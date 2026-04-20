package main

import (
	"flag"
	"fmt"
	"log"
	"os"

	"dns-resolver/cache"
	"dns-resolver/resolver"
	"dns-resolver/server"
)

func main() {
	httpAddr := flag.String("http", ":8053", "HTTP API listen address")
	logLevel := flag.String("log", "info", "Log level: debug, info, error")
	flag.Parse()

	logger := log.New(os.Stdout, "", log.LstdFlags)

	fmt.Println(`
 ____  _   _ ____    ____                 _
|  _ \| \ | / ___|  |  _ \ ___  ___  ___| |_   _____ _ __
| | | |  \| \___ \  | |_) / _ \/ __|/ _ \ \ \ / / _ \ '__|
| |_| | |\  |___) | |  _ <  __/\__ \  __/ |\ V /  __/ |
|____/|_| \_|____/  |_| \_\___||___/\___|_| \_/ \___|_|

Recursive DNS Resolver — Computer Networks Project
`)

	c := cache.New()
	res := resolver.New(c, logger, *logLevel)
	srv := server.New(res, c, logger, *httpAddr)

	logger.Printf("[INFO] Starting HTTP API on %s", *httpAddr)
	if err := srv.Start(); err != nil {
		logger.Fatalf("[FATAL] Server error: %v", err)
	}
}
