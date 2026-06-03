FROM golang:1.26 AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /app/server ./cmd/server/
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o /app/lb ./cmd/lb/

COPY resources/ resources/
RUN /app/server -build-index-in resources/references.json.gz -build-index-out resources/index.bin

FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/server .
COPY --from=builder /app/lb .
COPY --from=builder /app/resources/index.bin resources/
COPY resources/normalization.json resources/
COPY resources/mcc_risk.json resources/
CMD ["./server"]
