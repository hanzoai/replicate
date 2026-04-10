FROM golang:1.25 AS builder

# Install build dependencies for VFS extension
RUN apt-get update && apt-get install -y gcc libc6-dev && rm -rf /var/lib/apt/lists/*

WORKDIR /src/replicate
COPY . .

ARG REPLICATE_VERSION=latest

# Build replicate binary
RUN --mount=type=cache,target=/root/.cache/go-build \
	--mount=type=cache,target=/go/pkg \
	go build -ldflags "-s -w -X 'main.Version=${REPLICATE_VERSION}' -extldflags '-static'" -tags osusergo,netgo,sqlite_omit_load_extension -o /usr/local/bin/replicate ./cmd/replicate

# Build VFS loadable extension
RUN --mount=type=cache,target=/root/.cache/go-build \
	--mount=type=cache,target=/go/pkg \
	mkdir -p dist && \
	CGO_ENABLED=1 go build \
	-tags "vfs,SQLITE3VFS_LOADABLE_EXT" \
	-buildmode=c-archive \
	-o dist/replicate-vfs.a ./cmd/replicate-vfs && \
	mv dist/replicate-vfs.h src/replicate-vfs.h && \
	gcc -DSQLITE3VFS_LOADABLE_EXT -g -fPIC -shared \
	-o dist/replicate-vfs.so \
	src/replicate-vfs.c \
	dist/replicate-vfs.a \
	-lpthread -ldl -lm

FROM debian:bookworm-slim

RUN apt-get update && \
	apt-get install -y ca-certificates sqlite3 && \
	rm -rf /var/lib/apt/lists/*

COPY --from=builder /usr/local/bin/replicate /usr/local/bin/replicate
COPY --from=builder /src/replicate/dist/replicate-vfs.so /usr/local/lib/replicate-vfs.so

ENTRYPOINT ["/usr/local/bin/replicate"]
CMD []
