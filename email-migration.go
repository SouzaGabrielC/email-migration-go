package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/emersion/go-imap"
	quota "github.com/emersion/go-imap-quota"
	"github.com/emersion/go-imap/client"
)

type MigrateEmail struct {
	FromProvider Provider
	ToProvider   Provider
	FromEmail    Email
	ToEmail      Email
}

func createFileForLogger(email string) (*os.File, error) {
	dt := time.Now()
	file, err := os.OpenFile(fmt.Sprintf("./logs/%s-%s.log", dt.Format(time.RFC3339), email), os.O_RDWR|os.O_CREATE|os.O_APPEND, 0666)

	if err != nil {
		return nil, err
	}

	return file, nil
}

func closeFile(f *os.File) {
	err := f.Close()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func getQuotaOfEmail(client *client.Client) (used uint32, available uint32, err error) {
	qc := quota.NewClient(client)

	supports, err := qc.SupportQuota()
	// Check for server support
	if !supports || err != nil {
		return 0, 0, errors.New("Client doesn't support QUOTA extension")
	}

	quotas, err := qc.GetQuotaRoot("*")

	if err != nil {
		return 0, 0, err
	}

	// Print quotas
	used = quotas[0].Resources["STORAGE"][0]
	available = quotas[0].Resources["STORAGE"][1]

	return
}

func findMailboxOrCreateIt(mailboxName string, client *client.Client) (*imap.MailboxStatus, error) {
	status, err := client.Select(mailboxName, false)

	if err != nil {
		err = client.Create(mailboxName)
		if err != nil {
			return nil, err
		}

		status, _ = client.Select(mailboxName, false)
	}

	return status, nil
}

func findSentMailboxName(client *client.Client) (string, error) {
	toMailboxes := make(chan *imap.MailboxInfo, 10)
	done := make(chan error, 1)

	go func() {
		done <- client.List("", "*", toMailboxes)
	}()

	for toMailbox := range toMailboxes {
		for _, flag := range toMailbox.Attributes {
			if flag == "\\Sent" {
				return toMailbox.Name, nil
			}
		}
	}

	if err := <-done; err != nil {
		return "", err
	}

	return "", nil
}

func migrateEmail(migrateData MigrateEmail) {
	logger := log.New()

	file, err := createFileForLogger(migrateData.FromEmail.Email)

	defer closeFile(file)

	if err != nil {
		log.Printf("Failed to create log file for %s to %s", migrateData.FromEmail.Email, migrateData.ToEmail.Email)
		return
	}

	logger.Out = io.Writer(file)

	fromAddress := fmt.Sprintf("%s:%d", migrateData.FromProvider.Host, migrateData.FromProvider.Port)
	toAddress := fmt.Sprintf("%s:%d", migrateData.ToProvider.Host, migrateData.ToProvider.Port)

	loggerFrom := logger.WithFields(log.Fields{
		"provider": "from",
		"email":    migrateData.FromEmail.Email,
	})

	loggerTo := logger.WithFields(log.Fields{
		"provider": "to",
		"email":    migrateData.ToEmail.Email,
	})

	fromClient, err := client.DialTLS(fromAddress, nil)

	if err != nil {
		loggerFrom.Errorf("Erro trying to connect: %s", err)
		return
	}

	defer fromClient.Logout()

	toClient, err := client.Dial(toAddress)

	if err != nil {
		loggerTo.Errorf("Erro trying to connect: %s", err)
		return
	}

	defer toClient.Logout()

	emailTo := migrateData.ToEmail
	emailFrom := migrateData.FromEmail

	logger.Printf("Migrating\n\t- From: %s\n\t- To: %s", emailFrom.Email, emailTo.Email)

	if err := fromClient.Login(emailFrom.Email, emailFrom.Password); err != nil {
		loggerFrom.Error("Erro trying to login using the email and password")
		return
	}

	if err := toClient.Login(emailTo.Email, emailTo.Password); err != nil {
		loggerTo.Error("Erro trying to login using the email and password")
		return
	}

	logger.Println("Logged in on both e-mails")

	//Getting Quotas
	_, toAvailableQuota, _ := getQuotaOfEmail(toClient)

	fromUsedQuota, _, _ := getQuotaOfEmail(fromClient)

	logger.Printf("Used quota of from: %d, available quota of to: %d", fromUsedQuota, toAvailableQuota)

	if toAvailableQuota > fromUsedQuota {
		fromMailboxes := make(chan *imap.MailboxInfo, 10)
		done := make(chan error, 1)

		go func() {
			done <- fromClient.List("", "*", fromMailboxes)
		}()

		loggerFrom.Println("Mailboxes:")

	mailboxesLoop:
		for m := range fromMailboxes {
			// Skip junk and trash mailboxes and get Sent flag
			var isSentMailbox bool = false
			for _, flag := range m.Attributes {
				if flag == "\\Junk" || flag == "\\Trash" {
					continue mailboxesLoop
				}

				if flag == "\\Sent" {
					isSentMailbox = true
				}
			}

			loggerFrom.Printf("From Mailbox: %s", m.Name)

			var toMailBoxName string

			_, err := fromClient.Select(m.Name, true)

			if err != nil {
				loggerTo.Errorf("Error trying to select mailbox(%s): %s", m.Name, err.Error())
				continue mailboxesLoop
			}

			totalMessagesofFrom := fromClient.Mailbox().Messages

			if totalMessagesofFrom > 0 {

				if isSentMailbox {
					mailboxName, err := findSentMailboxName(toClient)

					if err != nil {
						loggerTo.Errorf("Error trying to find sent mailbox: %s", err.Error())
						continue mailboxesLoop
					}

					toMailboxStatus, err := toClient.Select(mailboxName, false)

					if err != nil {
						loggerTo.Errorf("Error trying to select sent mailbox(%s): %s", mailboxName, err.Error())
						continue mailboxesLoop
					}

					toMailBoxName = toMailboxStatus.Name
				} else {
					toMailboxStatus, err := findMailboxOrCreateIt(m.Name, toClient)

					if err != nil {
						loggerTo.Errorf("Error trying to find/create/select mailbox(%s): %s", m.Name, err.Error())
						continue mailboxesLoop
					}

					toMailBoxName = toMailboxStatus.Name
				}

				loggerTo.Printf("To Mailbox: %s", toMailBoxName)

				loggerFrom.Printf("Total of messages to transfer: %d", totalMessagesofFrom)

				seqset := new(imap.SeqSet)
				seqset.AddRange(1, totalMessagesofFrom)

				messages := make(chan *imap.Message, totalMessagesofFrom)
				fetchDone := make(chan error, 1)

				section := &imap.BodySectionName{}

				go func() {
					fetchDone <- fromClient.Fetch(seqset, []imap.FetchItem{
						imap.FetchFlags,
						imap.FetchInternalDate,
						section.FetchItem(),
					}, messages)
				}()

				messagesTransferedCount := 0
				for msg := range messages {
					err := toClient.Append(toMailBoxName, msg.Flags, msg.InternalDate, msg.GetBody(section))
					if err == nil {
						messagesTransferedCount += 1
					}
				}

				if err := <-fetchDone; err != nil {
					loggerFrom.Errorf("Error trying to fetch messages from mailbox: %s", err)
				}

				loggerTo.Printf("Messages transfered for mailbox(%s): %d of %d", toMailBoxName, messagesTransferedCount, totalMessagesofFrom)
			}
		}

		if err := <-done; err != nil {
			loggerFrom.Errorf("Error trying to get mailboxes: %s", err)
		}
	} else {
		loggerTo.Error("Unable to tranfer e-mail. Used quota of fromEmail is greater than the available quota of toEmail.")
	}
}
