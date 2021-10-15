package main

import (
	"log"
	"sync"
)

func main() {

	log.Println("Getting data from JSON")
	transferData, err := getTransferDataFromJson()

	if err != nil {
		log.Fatalf("Error trying to ge JSON data: %s", err.Error())
	}

	wg := new(sync.WaitGroup)

	for emailIndex, emailFrom := range transferData.From.Emails {
		wg.Add(1)
		go func(emailFrom Email, emailIndex int) {
			log.Printf("Started %s to %s", emailFrom, transferData.To.Emails[emailIndex])
			defer wg.Done()
			migrateEmail(MigrateEmail{
				FromProvider: transferData.From.Provider,
				ToProvider:   transferData.To.Provider,
				FromEmail:    emailFrom,
				ToEmail:      transferData.To.Emails[emailIndex],
			})
			log.Printf("Finished %s to %s", emailFrom, transferData.To.Emails[emailIndex])
		}(emailFrom, emailIndex)
	}

	wg.Wait()

	log.Println("Done!")
}
