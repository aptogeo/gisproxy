package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/aptogeo/gisproxy/lib"
)

func main() {
	var listen string
	var prefix string
	flag.StringVar(&listen, "listen", "localhost:8181", "host:port to listen on")
	flag.StringVar(&prefix, "prefix", "/", "prefix path")
	log.Println("Listen:", listen, "Prefix:", prefix)
	gisProxy := lib.NewGisProxy(prefix)
	gisProxy.SetBeforeSendFunc(func(gisInfo *lib.GisInfo, req *http.Request) (*http.Request, error) {
		log.Println(gisInfo)
		return req, nil
	})
	http.HandleFunc(prefix, gisProxy.ServeHTTP)
	http.ListenAndServe(listen, nil)
}
