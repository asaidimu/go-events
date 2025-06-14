.PHONY: all build test clean

all: build

build:
	go build -v ./...

test:
	go test -v ./...

clean:
	rm -f go-events
