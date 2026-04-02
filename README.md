# retina-generator

`retina-generator` is a CLI tool that generates Probing Directives (PDs) from CLI arguments and writes them to a JSONL output file.

**Part of the Retina system:**
- **Generator**: Creates probing directives (this component)
- **Orchestrator**: Distributes directives to agents, collects FIEs
- **Agent**: Executes network probes

## Build

```bash
make build
```

To build only the binary:
```bash
make build_gen
```

To clean:
```bash
make clean
```

## Test

```bash
make test
```

## Usage

Agent IDs are provided as positional arguments:

```bash
./retina-generator [flags] <agentID> [agentID...]
```

### Example

```bash
./retina-generator \
  --seed=123 \
  --min-ttl=4 \
  --max-ttl=20 \
  --num-pds=100 \
  --output-file=directives.jsonl \
  --log-level=info \
  a1 a2 a3
```

## Flags

| Flag               | Default | Description                                  |
| ------------------ | ------- | -------------------------------------------- |
| `--seed`           | `42`    | Seed for the random generator                |
| `--min-ttl`        | `1`     | Minimum TTL (0-255)                          |
| `--max-ttl`        | `32`    | Maximum TTL (0-255)                          |
| `--num-pds`        | `100`   | Number of PDs to generate                    |
| `--output-file`    | `""`    | Path to the output JSONL file for the PDs    |
| `--blocklist-file` | `""`    | Path to a file of CIDR networks to exclude   |
| `--log-level`      | `info`  | Log level (`debug`, `info`, `warn`, `error`) |

## Behavior

- The generator runs once: generate → write to file → exit.
- The output is written in JSONL format to the path specified by `--output-file`.
- `--output-file` is required; the program will exit with an error if it is not set.
- The same seed always produces the same set of PDs, useful for debugging and replay.
- `--min-ttl` and `--max-ttl` must be between 0 and 255.
- Logs are written to stdout in JSON format, compatible with Loki/Grafana pipelines.
- The program handles `SIGINT` and `SIGTERM` for graceful shutdown.

## License

MIT License - see [LICENSE](LICENSE) for details