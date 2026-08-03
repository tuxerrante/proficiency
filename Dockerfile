FROM golang:1.26.5-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.Version=${VERSION}" \
    -o /out/proficiency ./cmd/proficiency

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/proficiency /usr/local/bin/proficiency
WORKDIR /work
ENTRYPOINT ["/usr/local/bin/proficiency"]
