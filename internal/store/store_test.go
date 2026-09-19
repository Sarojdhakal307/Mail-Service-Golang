package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
)

// Integration tests; run with TEST_DATABASE_URL pointing at a disposable Postgres database.
func openTestStore(t *testing.T) *Store {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	c, err := NewCipher(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	s, err := Open(context.Background(), url, c)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestReserveEnforcesLimits(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	key, raw, err := s.CreateKey(ctx, KeyInput{Name: "limited", Limits: Limits{Hour: 5, Day: 8}}, "127.0.0.1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { s.DeleteKey(ctx, key.ID) })
	if !strings.HasPrefix(raw, "msk_") || len(raw) != 68 {
		t.Fatalf("unexpected key format %q", raw)
	}
	found, err := s.FindKeyByHash(ctx, HashKey(raw))
	if err != nil || found.ID != key.ID {
		t.Fatalf("lookup by hash: %v", err)
	}
	if !found.Recoverable {
		t.Fatal("new key should be recoverable")
	}
	revealed, err := s.RevealKey(ctx, key.ID)
	if err != nil || revealed != raw {
		t.Fatalf("reveal: got %q, %v", revealed, err)
	}

	entry := RequestLog{IP: "127.0.0.1", Method: "POST", Path: "/send/bulk"}
	if err := s.Reserve(ctx, key, entry, 3); err != nil {
		t.Fatalf("first reserve: %v", err)
	}

	err = s.Reserve(ctx, key, entry, 3)
	var limitErr *LimitError
	if !errors.As(err, &limitErr) {
		t.Fatalf("expected LimitError, got %v", err)
	}
	if limitErr.Window != "hour" || limitErr.Used != 3 || limitErr.Limit != 5 || limitErr.RetryAt == nil {
		t.Fatalf("unexpected limit error: %+v", limitErr)
	}
	if !strings.Contains(limitErr.Error(), "Hourly limit exceeded") {
		t.Fatalf("unexpected message: %s", limitErr.Error())
	}

	if err := s.Reserve(ctx, key, entry, 2); err != nil {
		t.Fatalf("reserve up to the limit: %v", err)
	}

	err = s.Reserve(ctx, key, entry, 9)
	if !errors.As(err, &limitErr) || limitErr.RetryAt != nil || !strings.Contains(err.Error(), "split it") {
		t.Fatalf("expected oversized request error, got %v", err)
	}

	logs, err := s.ListLogs(ctx, key.ID, 10)
	if err != nil {
		t.Fatalf("logs: %v", err)
	}
	accepted, rejected := 0, 0
	for _, l := range logs {
		switch l.Status {
		case StatusAccepted:
			accepted += l.MailCount
		case StatusLimitExceeded:
			rejected++
		}
	}
	if accepted != 5 || rejected != 2 {
		t.Fatalf("got %d accepted mails and %d rejections, want 5 and 2", accepted, rejected)
	}
}

func TestReserveIsSafeUnderConcurrency(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	key, _, err := s.CreateKey(ctx, KeyInput{Name: "concurrent", Limits: Limits{Hour: 10}}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { s.DeleteKey(ctx, key.ID) })

	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s.Reserve(ctx, key, RequestLog{IP: "127.0.0.1", Method: "POST", Path: "/send"}, 1) == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 10 {
		t.Fatalf("expected exactly 10 accepted requests, got %d", ok)
	}
}

func TestSuperKeyBypassesLimits(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	raw := "super-test-key-" + strings.Repeat("x", 32)
	if err := s.EnsureSuperKey(ctx, raw); err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if err := s.EnsureSuperKey(ctx, raw); err != nil {
		t.Fatalf("ensure is not idempotent: %v", err)
	}
	key, err := s.FindKeyByHash(ctx, HashKey(raw))
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	t.Cleanup(func() { s.DeleteKey(ctx, key.ID) })

	if !key.IsSuper || !key.AllowsIP("198.51.100.7") {
		t.Fatalf("super key should allow any IP: %+v", key)
	}
	if err := s.Reserve(ctx, key, RequestLog{IP: "198.51.100.7", Method: "POST", Path: "/send/bulk"}, 100000); err != nil {
		t.Fatalf("super key was limited: %v", err)
	}
}

func TestSyncConfigSuperKeyRevokesOldValues(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	oldRaw := "old-super-key-" + strings.Repeat("o", 32)
	newRaw := "new-super-key-" + strings.Repeat("n", 32)
	if _, err := s.SyncConfigSuperKey(ctx, oldRaw); err != nil {
		t.Fatal(err)
	}
	revoked, err := s.SyncConfigSuperKey(ctx, newRaw)
	if err != nil || revoked != 1 {
		t.Fatalf("rotate: revoked %d, %v", revoked, err)
	}
	oldKey, _ := s.FindKeyByHash(ctx, HashKey(oldRaw))
	newKey, _ := s.FindKeyByHash(ctx, HashKey(newRaw))
	t.Cleanup(func() { s.DeleteKey(ctx, oldKey.ID); s.DeleteKey(ctx, newKey.ID) })
	if oldKey.Active || !newKey.Active {
		t.Fatalf("after rotation old active=%v new active=%v", oldKey.Active, newKey.Active)
	}

	if _, err := s.SyncConfigSuperKey(ctx, ""); err != nil {
		t.Fatal(err)
	}
	newKey, _ = s.FindKeyByHash(ctx, HashKey(newRaw))
	if newKey.Active {
		t.Fatal("clearing SUPER_API_KEY should disable the configured super key")
	}
}

func TestKeyInputValidation(t *testing.T) {
	cases := []struct {
		in      KeyInput
		wantErr bool
		wantIPs string
	}{
		{KeyInput{Name: "a"}, false, "*"},
		{KeyInput{Name: "a", AllowedIPs: []string{"10.0.0.1", " 10.1.0.0/16 "}}, false, "10.0.0.1,10.1.0.0/16"},
		{KeyInput{Name: "a", AllowedIPs: []string{"10.0.0.1", "*"}}, false, "*"},
		{KeyInput{Name: "a", AllowedIPs: []string{"not-an-ip"}}, true, ""},
		{KeyInput{Name: "  "}, true, ""},
		{KeyInput{Name: "a", Limits: Limits{Day: -1}}, true, ""},
	}
	for i, tc := range cases {
		err := tc.in.Normalize()
		if (err != nil) != tc.wantErr {
			t.Errorf("case %d: err = %v", i, err)
			continue
		}
		if !tc.wantErr && strings.Join(tc.in.AllowedIPs, ",") != tc.wantIPs {
			t.Errorf("case %d: ips = %v", i, tc.in.AllowedIPs)
		}
	}
}

func TestCipherBindsCiphertextToKeyHash(t *testing.T) {
	c, err := NewCipher(strings.Repeat("01", 32))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := c.Seal("msk_secret", []byte("hash-a"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Open(sealed, []byte("hash-a")); err != nil || got != "msk_secret" {
		t.Fatalf("open: %q, %v", got, err)
	}
	if _, err := c.Open(sealed, []byte("hash-b")); err == nil {
		t.Fatal("ciphertext opened with the wrong key hash")
	}
	other, _ := NewCipher(strings.Repeat("02", 32))
	if _, err := other.Open(sealed, []byte("hash-a")); err == nil {
		t.Fatal("ciphertext opened with the wrong encryption key")
	}
	if _, err := NewCipher("too-short"); err == nil {
		t.Fatal("expected error for invalid key")
	}
}

func TestKeyRequestApproveAndReject(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, err := s.CreateKeyRequest(ctx, KeyRequestInput{Name: "Ada", Email: "not-an-email", Phone: "+977 9812345678", UseCase: strings.Repeat("x", 30)}, "1.2.3.4", ""); err == nil {
		t.Fatal("expected invalid email to be rejected")
	}
	if _, err := s.CreateKeyRequest(ctx, KeyRequestInput{Name: "Ada", Email: "ada@example.com", Phone: "+977 9812345678", UseCase: "short"}, "1.2.3.4", ""); err == nil {
		t.Fatal("expected short use case to be rejected")
	}

	for _, phone := range []string{"", "call me", "12345", "+1 234 567 890 123 456 7"} {
		bad := KeyRequestInput{Name: "Ada", Email: "ada@example.com", Phone: phone, UseCase: strings.Repeat("x", 30)}
		if _, err := s.CreateKeyRequest(ctx, bad, "1.2.3.4", ""); err == nil {
			t.Fatalf("expected phone %q to be rejected", phone)
		}
	}

	in := KeyRequestInput{Name: " Ada ", Email: "ada@example.com", Phone: " +977 (98) 1234-5678 ",
		UseCase: "Password reset emails for our store", CallerIPs: "10.0.0.1", Message: "Can we get a higher limit later?"}
	req, err := s.CreateKeyRequest(ctx, in, "1.2.3.4", "test-agent")
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if req.Name != "Ada" || req.Phone != "+977 (98) 1234-5678" || req.Message != in.Message || req.Status != RequestPending {
		t.Fatalf("unexpected request: %+v", req)
	}

	key, raw, err := s.ApproveKeyRequest(ctx, req.ID, KeyInput{Name: "Ada", AllowedIPs: []string{"10.0.0.1"}, Limits: Limits{Hour: 10}}, "127.0.0.1")
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	t.Cleanup(func() { s.DeleteKey(ctx, key.ID) })
	if raw == "" || key.Limits.Hour != 10 {
		t.Fatalf("unexpected key: %+v", key)
	}
	if _, _, err := s.ApproveKeyRequest(ctx, req.ID, KeyInput{Name: "again"}, ""); !errors.Is(err, ErrAlreadyReviewed) {
		t.Fatalf("second approve: got %v, want ErrAlreadyReviewed", err)
	}
	if err := s.RejectKeyRequest(ctx, req.ID); !errors.Is(err, ErrAlreadyReviewed) {
		t.Fatalf("reject after approve: got %v", err)
	}
	if err := s.RejectKeyRequest(ctx, 999999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("reject missing: got %v", err)
	}

	// A failed approval (invalid key input) must leave the request pending.
	req2, _ := s.CreateKeyRequest(ctx, in, "1.2.3.4", "")
	if _, _, err := s.ApproveKeyRequest(ctx, req2.ID, KeyInput{Name: ""}, ""); err == nil {
		t.Fatal("expected validation error")
	}
	if err := s.RejectKeyRequest(ctx, req2.ID); err != nil {
		t.Fatalf("reject pending request: %v", err)
	}

	list, err := s.ListKeyRequests(ctx)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[int64]string{}
	for _, r := range list {
		statuses[r.ID] = r.Status
	}
	if statuses[req.ID] != RequestApproved || statuses[req2.ID] != RequestRejected {
		t.Fatalf("unexpected statuses: %v", statuses)
	}
}

func TestKeySMTPSettings(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	if _, _, err := s.CreateKey(ctx, KeyInput{Name: "bad", SMTP: &SMTPInput{Host: "smtp.example.com", From: "not-an-email"}}, ""); err == nil {
		t.Fatal("expected invalid From to be rejected")
	}
	if _, _, err := s.CreateKey(ctx, KeyInput{Name: "bad", SMTP: &SMTPInput{Host: "smtp.example.com", Port: "99999", From: "a@b.com"}}, ""); err == nil {
		t.Fatal("expected invalid port to be rejected")
	}

	key, _, err := s.CreateKey(ctx, KeyInput{Name: "smtp", SMTP: &SMTPInput{
		Host: " smtp.example.com ", Username: "user@example.com", Password: "s3cret", From: "Acme <no-reply@example.com>",
	}}, "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { s.DeleteKey(ctx, key.ID) })
	if key.SMTP == nil || key.SMTP.Host != "smtp.example.com" || key.SMTP.Port != "587" || !key.SMTP.HasPassword {
		t.Fatalf("unexpected SMTP settings: %+v", key.SMTP)
	}
	if b, _ := json.Marshal(key); strings.Contains(string(b), "s3cret") {
		t.Fatal("SMTP password leaked into JSON")
	}

	cfg, err := s.SMTPConfig(key)
	if err != nil || cfg.Password != "s3cret" || cfg.From != "Acme <no-reply@example.com>" {
		t.Fatalf("SMTPConfig: %+v, %v", cfg, err)
	}

	// Empty password on update keeps the stored one.
	key, err = s.UpdateKey(ctx, key.ID, KeyInput{Name: "smtp", SMTP: &SMTPInput{
		Host: "smtp.example.com", Port: "465", Username: "user@example.com", From: "no-reply@example.com",
	}})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if cfg, _ := s.SMTPConfig(key); cfg.Password != "s3cret" || cfg.Port != "465" {
		t.Fatalf("password not kept: %+v", cfg)
	}

	// Removing the username clears the password.
	key, _ = s.UpdateKey(ctx, key.ID, KeyInput{Name: "smtp", SMTP: &SMTPInput{Host: "relay.local", From: "no-reply@example.com"}})
	if key.SMTP.HasPassword {
		t.Fatal("password should be cleared without a username")
	}

	// No SMTP input means the key falls back to the default server.
	key, _ = s.UpdateKey(ctx, key.ID, KeyInput{Name: "smtp"})
	if key.SMTP != nil {
		t.Fatalf("SMTP should be cleared: %+v", key.SMTP)
	}
	if cfg, err := s.SMTPConfig(key); cfg != nil || err != nil {
		t.Fatalf("expected default SMTP, got %+v, %v", cfg, err)
	}
}

func TestMailHistory(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	key, _, err := s.CreateKey(ctx, KeyInput{Name: "history"}, "127.0.0.1")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { s.DeleteKey(ctx, key.ID) })

	rec := MailRecord{APIKeyID: key.ID, KeyName: key.Name, Source: "api", Path: "/send/bulk", IP: "127.0.0.1",
		Sender: "no-reply@example.com", SMTPHost: "smtp.example.com:587", Subject: "Hello", Body: "Body text"}
	recipients := []string{"a@example.com", "b@example.com", "c@example.com"}
	ids, err := s.RecordMail(ctx, rec, recipients)
	if err != nil || len(ids) != 3 {
		t.Fatalf("record: %v, %v", ids, err)
	}
	for i, id := range ids {
		d, err := s.GetMail(ctx, id)
		if err != nil {
			t.Fatalf("get %d: %v", id, err)
		}
		if d.Recipient != recipients[i] || d.Body != "Body text" || d.Status != MailQueued || d.Sender != rec.Sender {
			t.Fatalf("delivery %d does not match recipient %d: %+v", id, i, d)
		}
	}

	if err := s.MarkDelivery(ctx, ids[0], MailSending, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDelivery(ctx, ids[0], MailSent, ""); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDelivery(ctx, ids[1], MailFailed, "550 mailbox unavailable"); err != nil {
		t.Fatal(err)
	}
	sent, _ := s.GetMail(ctx, ids[0])
	if sent.Status != MailSent || sent.SentAt == nil || sent.Attempts != 1 {
		t.Fatalf("expected sent with one attempt, got %+v", sent)
	}

	if err := s.RequeueDelivery(ctx, ids[0], "x@example.com", "h:25"); !errors.Is(err, ErrNotRetryable) {
		t.Fatalf("expected sent mail not to be retryable, got %v", err)
	}
	if err := s.RequeueDelivery(ctx, ids[1], "new@example.com", "smtp2.example.com:465"); err != nil {
		t.Fatalf("requeue failed mail: %v", err)
	}
	retried, _ := s.GetMail(ctx, ids[1])
	if retried.Status != MailQueued || retried.Error != "" || retried.Sender != "new@example.com" {
		t.Fatalf("expected requeued mail with new sender, got %+v", retried)
	}

	list, err := s.ListMails(ctx, MailFilter{KeyID: key.ID})
	if err != nil || len(list) != 3 || list[0].ID != ids[2] || list[0].Body != "" {
		t.Fatalf("expected 3 mails newest first without bodies, got %d, %v", len(list), err)
	}
	found, err := s.ListMails(ctx, MailFilter{KeyID: key.ID, Query: "B@EXAMPLE"})
	if err != nil || len(found) != 1 || found[0].ID != ids[1] {
		t.Fatalf("expected case-insensitive search to find b@, got %v, %v", found, err)
	}
	page, err := s.ListMails(ctx, MailFilter{KeyID: key.ID, BeforeID: ids[2], Limit: 1})
	if err != nil || len(page) != 1 || page[0].ID != ids[1] {
		t.Fatalf("expected paging before the newest mail, got %v, %v", page, err)
	}
	counts, err := s.MailCounts(ctx, MailFilter{KeyID: key.ID, Status: MailSent})
	if err != nil || counts[MailSent] != 1 || counts[MailQueued] != 2 {
		t.Fatalf("unexpected counts %v, %v", counts, err)
	}

	// History outlives the key that sent it.
	if err := s.DeleteKey(ctx, key.ID); err != nil {
		t.Fatal(err)
	}
	kept, err := s.GetMail(ctx, ids[0])
	if err != nil || kept.APIKeyID != nil || kept.KeyName != "history" {
		t.Fatalf("expected mail kept after key deletion, got %+v, %v", kept, err)
	}
	t.Cleanup(func() { s.db.Exec(`DELETE FROM mail_messages WHERE id = $1`, kept.MailID) })
}
