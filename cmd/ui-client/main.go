package main

import (
	"flag"
	"log"
	"net/http"
)

func main() {
	listen := flag.String("listen", ":8080", "client UI listen address")
	flag.Parse()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "cmd/ui-client/index.html")
	})

	log.Printf("UI client running on http://localhost%s", *listen)
	log.Fatal(http.ListenAndServe(*listen, nil))
}