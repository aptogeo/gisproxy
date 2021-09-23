package main

import (
	"flag"
	"log"
	"net/http"
	"runtime"

	"github.com/aptogeo/gisproxy/lib"
)

func main() {
	var listen string
	flag.StringVar(&listen, "listen", "", "host:port to listen on")

	var prefix string
	flag.StringVar(&prefix, "prefix", "/", "prefix path")

	var allowcrossorigin bool
	var https bool
	var crtfile string
	var keyfile string
	var gomaxprocs int
	flag.BoolVar(&allowcrossorigin, "allowcrossorigin", true, "allow cross origin")
	flag.BoolVar(&https, "https", false, "use https")
	flag.StringVar(&crtfile, "crtfile", "", "crt file")
	flag.StringVar(&keyfile, "keyfile", "", "key file")
	flag.IntVar(&gomaxprocs, "gomaxprocs", 4, "maximum number of CPUs")

	flag.Parse()

	if listen == "" {
		log.Fatalln("missing required -listen argument")
	}

	runtime.GOMAXPROCS(gomaxprocs)

	gisProxy := lib.NewGisProxy(listen, prefix, allowcrossorigin)
	if https {
		gisProxy.UseHttps(crtfile, keyfile)
	}

	gisProxy.SetBeforeSendFunc(func(writer http.ResponseWriter, request *http.Request) error {
		log.Println(lib.GisInfoFromContext(request.Context()))
		return nil
	})

	gisProxy.SetAfterReceiveFunc(func(writer http.ResponseWriter, response *http.Response) error {
		log.Println(response.StatusCode, response.Request.Method, response.Request.URL)
		return nil
	})

	if err := gisProxy.Start(); err != nil {
		log.Fatalln(err)
	}
}
