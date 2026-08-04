package main

import (
	"fmt"
	"log"
	"net/http"

	"mailservice/internal/routes"
	"mailservice/internal/services"
)

func main() {
	mailService := services.NewMailService(3, services.NewSMTPMailer())
	mailService.Start()

	mux := http.NewServeMux()
	routes.RegisterRoutes(mux, mailService)

	fmt.Println("Listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
