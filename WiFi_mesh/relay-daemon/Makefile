BINARY  = bitchat-relay
GOFLAGS = -ldflags="-s -w"

.PHONY: all build mips test clean

all: build

# Native build (for development/testing on your PC)
build:
	go build $(GOFLAGS) -o $(BINARY) .

# Cross-compile for GL.iNet Mango (MIPS little-endian, soft float)
mips:
	GOOS=linux GOARCH=mipsle GOMIPS=softfloat go build $(GOFLAGS) -o $(BINARY) .

test:
	go test -v -race ./...

clean:
	rm -f $(BINARY)
