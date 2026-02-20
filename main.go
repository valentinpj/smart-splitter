package main

import (
	"log"
	"net/http"

	"github.com/valentinpj/smart-splitter/api"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/split", api.HandleSplit)

	log.Println("Smart Order Splitter API listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
