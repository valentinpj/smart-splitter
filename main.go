package main

import (
	"log"
	"net/http"
	"os"

	"github.com/valentinpj/smart-splitter/api"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/split", api.HandleSplit)

	log.Printf("Smart Order Splitter API listening on :%s", port)
	log.Fatal(http.ListenAndServe(":"+port, mux))
}
