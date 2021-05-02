deps:
	go mod tidy
	go mod vendor

build: deps
	git rev-parse HEAD | cut -c 1-8 > .build-sha.txt
	scripts/update-version.sh
	go build .

all: build

install: build
	go install .
