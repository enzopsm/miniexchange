# Build stage
FROM golang:1.23-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /miniexchange ./cmd/miniexchange

# Final stage
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /miniexchange /miniexchange

EXPOSE 8080

ENTRYPOINT ["/miniexchange"]
