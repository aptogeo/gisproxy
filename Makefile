
PRG = gisproxy

build:
	@echo build
	@go install

run: build
	@echo run
	$$GOPATH/bin/$(PRG) -listen localhost:8181
