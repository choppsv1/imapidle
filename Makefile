deps:
	go mod tidy
	go mod vendor

build: deps
	git rev-parse HEAD | cut -c 1-8 > .build-sha.txt
	go run cmd/buildnumber.go
	go build .

all: build
