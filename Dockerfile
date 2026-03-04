FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /app ./cmd/paibot

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /app /app
COPY --from=build /src/config.yaml /etc/paibot/config.yaml
ENTRYPOINT ["/app"]
CMD ["--config", "/etc/paibot/config.yaml"]
