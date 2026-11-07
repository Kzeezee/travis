# Builds travis and the fake MCP server from the repo root into a plain image. Each sample's
# setup copies its fixture from /fixtures into /workspace, so one image serves every task.
FROM golang:1.27 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# eval tag: compiles in `travis run`, which release builds leave out (D-10).
RUN GOFLAGS=-tags=eval make build && go build -o bin/fakeserver ./internal/tools/mcp/testdata/fakeserver

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/bin/travis /src/bin/travis-broker /src/bin/fakeserver /usr/local/bin/
COPY testdata/fixtures /fixtures
COPY eval/inspect/skills /eval/skills
RUN mkdir /workspace
WORKDIR /workspace
CMD ["sleep", "infinity"]
