package main

import (
	"bufio"
	"log"
	"net/smtp"
	"os"
)

var numberTranslator = map[int]string{1: "One", 2: "Two", 3: "Three", 4: "Four", 5: "Five"}

func logConfigurations(configs map[string]string) {
	for k, v := range configs {
		log.Println(k, "-", v)
	}
}

func readClasses() []string {
	var classes []string
	file, err := os.Open("./models/coco.names.default")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)

	for scanner.Scan() {
		classes = append(classes, scanner.Text())
	}

	return classes
}

func sendMail(receiver string, title string, body string) {
	from := os.Getenv("EMAIL_ADDR")
	to := []string{receiver}
	smtpHost := os.Getenv("SMTP_HOST")
	message := []byte("Subject: " + title + "\r\n\r\n" + body + "\r\n")
	err := smtp.SendMail(smtpHost+":25", nil, from, to, message)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("Email notification of detected object has been sent to: %s", receiver)
}
