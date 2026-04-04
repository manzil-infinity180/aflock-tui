.PHONY: build run clean

build:
	go build -o aflock-tui .

run: build
	./aflock-tui

clean:
	rm -f aflock-tui
