FROM golang:1.26-alpine AS build
RUN apk add --no-cache gcc g++ musl-dev libde265-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -ldflags="-s -w -extldflags=-static" -o /app ./cmd/paibot

FROM scratch

# Build provenance (populated by CI build-args: GIT_COMMIT/GIT_BRANCH/BUILD_DATE)
ARG GIT_COMMIT
ARG GIT_BRANCH
ARG BUILD_DATE
LABEL org.opencontainers.image.source="https://github.com/NorskHelsenett/paibot" \
      org.opencontainers.image.revision="$GIT_COMMIT" \
      org.opencontainers.image.ref.name="$GIT_BRANCH" \
      org.opencontainers.image.created="$BUILD_DATE"

COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=build /app /app
COPY --from=build /src/config.yaml /etc/paibot/config.yaml

ENV TZ=Europe/Oslo

ENTRYPOINT ["/app"]
CMD ["--config", "/etc/paibot/config.yaml"]
