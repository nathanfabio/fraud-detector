FROM golang:1.26 AS builder

WORKDIR /app
COPY go.mod ./
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /app/server ./cmd/server/

COPY resources/ resources/
RUN /app/server -build-index-in resources/references.json.gz -build-index-out resources/index.bin

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/resources/index.bin resources/
COPY resources/normalization.json resources/
COPY resources/mcc_risk.json resources/
EXPOSE 8080
CMD ["./server"]
