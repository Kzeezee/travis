# Builds travis from the repo root and drops it into a plain image with the notes fixture.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# eval tag: compiles in `travis run`, which release builds leave out (D-10).
RUN GOFLAGS=-tags=eval make build

FROM debian:trixie-slim
COPY --from=build /src/bin/travis /src/bin/travis-broker /usr/local/bin/
COPY testdata/fixtures/notes /workspace
COPY eval/inspect/answers.json /eval/answers.json
WORKDIR /workspace
CMD ["sleep", "infinity"]
