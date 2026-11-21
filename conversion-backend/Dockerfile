FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 go build -trimpath -o /out/conversion ./cmd/conversion \
    && CGO_ENABLED=0 go build -trimpath -o /out/conversion-ops ./cmd/conversion-ops

FROM alpine:3.22
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/ /app/bin/
USER 65532:65532
ENTRYPOINT ["/app/bin/conversion"]
