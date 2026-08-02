package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Nciae-Zyh/stundeck/internal/engine"
)

func main() {
	if len(os.Args) < 7 {
		fail("natmap callback expected 6 arguments")
	}
	publicPort, err := strconv.Atoi(os.Args[2])
	if err != nil {
		fail("invalid public port")
	}
	privatePort, err := strconv.Atoi(os.Args[4])
	if err != nil {
		fail("invalid private port")
	}
	mapping := engine.Mapping{
		ServiceID:   os.Getenv("STUNDECK_SERVICE_ID"),
		PublicIP:    os.Args[1],
		PublicPort:  publicPort,
		IP4P:        os.Args[3],
		PrivatePort: privatePort,
		Protocol:    os.Args[5],
		PrivateIP:   os.Args[6],
	}
	if err := engine.ValidateMapping(mapping); err != nil {
		fail(err.Error())
	}
	payload, err := json.Marshal(mapping)
	if err != nil {
		fail("encode callback payload")
	}
	request, err := http.NewRequest(http.MethodPost, os.Getenv("STUNDECK_CALLBACK_URL"), bytes.NewReader(payload))
	if err != nil {
		fail("create callback request")
	}
	request.Header.Set("Authorization", "Bearer "+os.Getenv("STUNDECK_CALLBACK_TOKEN"))
	request.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		fail("send callback: " + err.Error())
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		fail("callback returned " + response.Status)
	}
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, "stundeck-notify:", message)
	os.Exit(1)
}
