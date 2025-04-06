FROM docker.io/golang:1.24-alpine3.21 AS build

ENV CGO_ENABLED=1

RUN apk add --no-cache gcc musl-dev

WORKDIR /usr/src/app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -v \
    -o /usr/local/bin/knotserver \
    -ldflags='-s -w -extldflags "-static"' \
    ./cmd/knotserver && \
    go build -v \
    -o /usr/local/bin/keyfetch \
    ./cmd/keyfetch && \
    go build -v \
    -o /usr/local/bin/repoguard \
    ./cmd/repoguard

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

COPY --from=build /usr/local/bin/knotserver /usr/local/bin
COPY --from=build /usr/local/bin/keyfetch /usr/local/libexec/tangled-keyfetch
COPY --from=build /usr/local/bin/repoguard /home/git/repoguard
COPY docker/rootfs/ .

RUN chown root:root /usr/local/libexec/tangled-keyfetch && \
    chmod 755 /usr/local/libexec/tangled-keyfetch && \
    chown git:git /home/git/repoguard && \
    chown git:git /app && chown git:git /home/git/repositories

EXPOSE 22
EXPOSE 5555

ENTRYPOINT ["/init"]
