FROM golang:1.26.5-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/udpreflector ./cmd/udpreflector

FROM scratch

USER 65532:65532
COPY --from=build /out/udpreflector /udpreflector
EXPOSE 9090/tcp
ENTRYPOINT ["/udpreflector"]
