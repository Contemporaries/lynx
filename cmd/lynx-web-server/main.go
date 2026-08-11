package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
)

//go:embed static/*
var staticFS embed.FS

func main() {
	listen := flag.String("listen", "127.0.0.1:9080", "HTTP listen address for Lynx server Web UI")
	flag.Parse()
	root, err := fs.Sub(staticFS, "static")
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("lynx-web-server listening on http://%s", *listen)
	log.Fatal(http.ListenAndServe(*listen, http.FileServer(http.FS(root))))
}
