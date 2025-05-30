FROM docker.io/golang:1.24-alpine3.21 AS build

ENV CGO_ENABLED=1
WORKDIR /usr/src/app
COPY go.mod go.sum ./

RUN apk add --no-cache gcc musl-dev
RUN go mod download

COPY . .
RUN go build -v \
    -o /usr/local/bin/knot \
    -ldflags='-s -w -extldflags "-static"' \
    ./cmd/knot

FROM docker.io/alpine:3.21

LABEL org.opencontainers.image.title=Tangled
LABEL org.opencontainers.image.description="Tangled is a decentralized and open code collaboration platform, built on atproto."
LABEL org.opencontainers.image.vendor=Tangled.sh
LABEL org.opencontainers.image.licenses=MIT
LABEL org.opencontainers.image.url=https://tangled.sh
LABEL org.opencontainers.image.source=https://tangled.sh/@tangled.sh/core

RUN apk add --no-cache shadow s6-overlay execline openssh git && \
    adduser --disabled-password git && \
    # We need to set password anyway since otherwise ssh won't work
    head -c 32 /dev/random | base64 | tr -dc 'a-zA-Z0-9' | passwd git --stdin && \
    mkdir /app && mkdir /home/git/repositories

COPY --from=build /usr/local/bin/knot /usr/local/bin
COPY docker/rootfs/ .

EXPOSE 22
EXPOSE 5555

ENTRYPOINT ["/bin/sh", "-c", "chown git:git /app && chown git:git /home/git/repositories && /init"]
