# retina-generator

`retina-generator` is a small CLI tool that generates Probing Directives (PDs) from CLI arguments and sends them to an orchestrator via HTTP (`POST /directives`). After sending the directives, the program exits.

**Part of the Retina system:**
- **Generator**: Creates probing directives (this component)
- **Orchestrator**: Distributes directives to agents, collects FIEs
- **Agent**: Executes network probes

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
  -min-ttl=4 \
  -max-ttl=20 \
  -num-pds=100 \
  -orchestrator-addr=http://localhost:8080 \
  -http-timeout=5s \
  a1 a2 a3
```

This will:
1. Generate 100 Probing Directives.
2. Assign them randomly to agents `a1`, `a2`, `a3`.
3. Send them to `http://localhost:8080/directives`.
4. Exit.

---

## Flags

| Flag                 | Default                 | Description                     |
| -------------------- | ----------------------- | ------------------------------- |
| `--seed`              | `42`                    | Seed for the random generator   |
| `--min-ttl`           | `4`                     | Minimum TTL (0-255)             |
| `--max-ttl`           | `32`                    | Maximum TTL (0-255)             |
| `--num-pds`           | `1000`                  | Number of PDs to generate       |
| `--orchestrator-addr` | `http://localhost:8080` | Orchestrator address            |
| `--http-timeout`      | `10s`                   | HTTP timeout (`0` = no timeout) |

---

## Behavior

- At least one agent ID must be provided as a positional argument.
- The orchestrator address must include the scheme (e.g. `http://`).
- The generator runs once: generate → send → exit.
- The orchestrator must expose `POST /directives` at the provided address.
- The same seed always produces the same set of PDs, useful for debugging and replay.
- If the orchestrator is unreachable, the generator exits with an error and must be re-run.

---

## License

MIT