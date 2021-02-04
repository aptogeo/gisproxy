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
	var allowCrossOrigin bool
	flag.StringVar(&listen, "listen", "localhost:8181", "host:port to listen on")
	flag.StringVar(&prefix, "prefix", "/", "prefix path")
	flag.BoolVar(&allowCrossOrigin, "allowCrossOrigin", true, "allow cross origin")
	flag.Parse()
	log.Println("Listen:", listen, "Prefix:", prefix)
	gisProxy := lib.NewGisProxy(prefix, allowCrossOrigin)
	gisProxy.SetBeforeSendFunc(func(gisInfo *lib.GisInfo, req *http.Request) (*http.Request, error) {
		log.Println(gisInfo)
		return req, nil
	})
	http.HandleFunc(prefix, gisProxy.ServeHTTP)
	http.ListenAndServe(listen, nil)
}
