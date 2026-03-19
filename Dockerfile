FROM golang:1.26-alpine AS build
RUN apk add --no-cache gcc g++ musl-dev libde265-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w -extldflags=-static" -o /app ./cmd/paibot

FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /app /app
COPY --from=build /src/config.yaml /etc/paibot/config.yaml

ENV TZ=Europe/Oslo

ENTRYPOINT ["/app"]
CMD ["--config", "/etc/paibot/config.yaml"]
