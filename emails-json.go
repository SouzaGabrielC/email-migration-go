package main

import (
	"encoding/json"
	"io/ioutil"
	"os"
)

type FromToTransfer struct {
	From EmailTransfer `json:"from"`
	To   EmailTransfer `json:"to"`
}

type EmailTransfer struct {
	Provider Provider `json:"provider"`
	Emails   []Email  `json:"emails"`
}

type Provider struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
}

type Email struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func getTransferDataFromJson() (FromToTransfer, error) {
	jsonFile, err := os.Open("../transferEmails.json")

	defer jsonFile.Close()

	if err != nil {
		return FromToTransfer{}, err
	}

	byteValue, _ := ioutil.ReadAll(jsonFile)

	var fromToTransfer FromToTransfer

	json.Unmarshal(byteValue, &fromToTransfer)

	return fromToTransfer, nil
}
