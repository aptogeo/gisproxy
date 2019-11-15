
PRG = gisproxy

build:
	@go install

run: build
	$$GOPATH/bin/$(PRG)
