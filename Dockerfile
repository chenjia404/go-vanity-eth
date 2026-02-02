FROM golang:1.25-alpine as builder

RUN apk add --no-cache gcc musl-dev linux-headers git

WORKDIR /build

COPY go.mod go.mod
COPY go.sum go.sum
RUN go mod download

COPY . ./

RUN  go build -trimpath -ldflags="-w -s" -o /build/go-vanity-eth .

FROM alpine:3

WORKDIR /app
RUN apk update --no-cache && apk upgrade && apk add --no-cache ca-certificates

COPY --from=builder /build/go-vanity-eth /app/go-vanity-eth


ENTRYPOINT   ["./go-vanity-eth"]
