# retina-generator

`retina-generator` is a small CLI tool that generates Probing Directives (PDs) from CLI arguments and sends them to an orchestrator via HTTP (`POST /directives`).
After sending the directives, the program exits.

---

## Build

Using the provided Makefile:

```bash
make build
```

This will:

- Format the code (`gofmt`, `goimports` if available)
- Run `golangci-lint`
- Build the binary `retina-generator`

To build only:

```bash
make build_gen
```

To clean:

```bash
make clean
```

---

## Test

```bash
make test
```

---

## Usage

Agent IDs are provided as positional arguments:

```bash
./retina-generator [flags] <agentID> [agentID...]
```

### Example

```bash
./retina-generator \
  -seed=123 \
  -min_ttl=4 \
  -max_ttl=20 \
  -num_pds=100 \
  -orchestrator=localhost:8080 \
  -http_timeout=5s \
  a1 a2 a3
```

This will:

1. Generate 100 Probing Directives.
2. Assign them to agents `a1`, `a2`, `a3`.
3. Send them to `http://localhost:8080/directives`.
4. Exit.

---

## Flags

| Flag            | Default          | Description                     |
| --------------- | ---------------- | ------------------------------- |
| `-seed`         | `42`             | Seed for the random generator   |
| `-min_ttl`      | `4`              | Minimum TTL                     |
| `-max_ttl`      | `32`             | Maximum TTL                     |
| `-num_pds`      | `1`              | Number of PDs to generate       |
| `-orchestrator` | `localhost:8080` | Orchestrator base URL           |
| `-http_timeout` | `10s`            | HTTP timeout (`0` = no timeout) |

---

## Behavior

- At least one agent ID must be provided.
- The generator runs once: generate → send → exit.
- The orchestrator must expose `POST /directives`.
