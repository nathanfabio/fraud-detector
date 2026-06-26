# Fraud Detector

Ultra-low-latency fraud detection microservice that classifies financial transactions using k-NN on an IVF (Inverted File) index of 14-dimensional feature vectors. Built for maximum throughput at minimal tail latency.

## Architecture

```
Client TCP ──► LB (epoll) ──FD pass over UDS──► Server (epoll)
                         ──FD pass over UDS──► Server (epoll)
```

- **LB** (`cmd/lb`): TCP load balancer using raw epoll + `SO_REUSEPORT`. Receives connections and passes file descriptors to backend servers via Unix domain sockets (`SCM_RIGHTS`).
- **Server** (`cmd/server`): Epoll-based HTTP server that receives FDs from the LB, parses HTTP manually, vectorizes the JSON request body, and searches the IVF index.
- **No `net/http`** in the hot path — custom HTTP parsing, raw syscalls (`SYS_RECVFROM`, `SYS_SENDTO`), real-time scheduling, and memory locking.

## How It Works

### Feature Vector (14 dimensions)

Each transaction is converted into a 14-dimensional `int16` vector normalized to `[0, 10000]`:

| Index | Feature | Description |
|-------|---------|-------------|
| 0 | Amount | Normalized transaction amount |
| 1 | Installments | Normalized installment count |
| 2 | Amount vs Average | Ratio of transaction amount to customer average |
| 3 | Hour of Day | 0–23 normalized |
| 4 | Day of Week | Mon=0..Sun=6 normalized |
| 5 | Time Since Last TX | Minutes since last transaction (-1 if none) |
| 6 | Distance From Last TX | Km from last transaction location (-1 if none) |
| 7 | Distance From Home | Km from home location |
| 8 | TX Count 24h | Transactions in the last 24 hours |
| 9 | Is Online | 0 or 10000 |
| 10 | Card Present | 0 or 10000 |
| 11 | Unknown Merchant | 0 or 10000 |
| 12 | MCC Risk | Pre-mapped merchant category risk |
| 13 | Merchant Avg Amount | Normalized merchant average transaction |

### IVF Index

- **16 partitions** based on 4 tag bits: card present, is online, unknown merchant, has last transaction
- Each partition uses **K-means++** clustering with mini-batch K-means
- Bounding-box pruning for fast cluster selection (`nprobe=18`)
- Early-exit Euclidean distance for top-5 nearest neighbors
- **Repair sweep**: if verdict is ambiguous (1–4 frauds in top-5), probes all remaining clusters for accuracy

### Classification

The 5 nearest neighbors' labels (`legit` / `fraud`) are counted. The fraud score is:

| Fraud Count | Score | Approved |
|-------------|-------|----------|
| 0 | 0.0 | Yes |
| 1 | 0.2 | Yes |
| 2 | 0.4 | Yes |
| 3 | 0.6 | No |
| 4 | 0.8 | No |
| 5 | 1.0 | No |

## Project Structure

```
├── cmd/
│   ├── lb/main.go          # TCP load balancer
│   └── server/main.go      # Epoll-based HTTP API server
├── internal/
│   ├── config/             # JSON config loading (normalization, MCC risk)
│   ├── handler/            # Fraud detection handler (Process, Score, Ready)
│   ├── index/              # IVF index: build, search, serialization
│   ├── netx/               # Epoll busy-poll, FD passing over Unix sockets
│   ├── server/             # Conn handler, FD listener
│   └── vectorizer/         # Fast JSON parser + vector builder
├── resources/
│   ├── normalization.json  # Normalization parameters
│   ├── mcc_risk.json       # MCC code → risk mapping
│   ├── references.json.gz  # Training data for index building
│   ├── index.bin           # Pre-built IVF index (binary)
│   └── example-references.json  # Small sample training set
├── test-data/
│   └── test-data.json      # 54,100 labeled test entries
├── docker-compose.yml
├── Dockerfile
├── nginx.conf
└── go.mod
```

## Requirements

- Go 1.26+
- Linux (uses `epoll`, `mlockall`, real-time scheduling, `SCM_RIGHTS`)

## Building

```sh
# Build the index from training data
go run ./cmd/server -build-index-in resources/references.json.gz -build-index-out resources/index.bin

# Build binaries
go build -o server ./cmd/server
go build -o lb ./cmd/lb
```

## Running

### Server

```sh
./server
```

The server binds to `/run/sock/$HOSTNAME.sock` and waits for the load balancer to connect and pass file descriptors.

### Load Balancer

```sh
./lb <port> <uds_path1> [uds_path2 ...]
```

Example:
```sh
./lb 9999 /run/sock/api1.sock /run/sock/api2.sock
```

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `INDEX_PATH` | `resources/index.bin` | Path to the IVF index binary |

## API

### `POST /fraud-score`

Request body (JSON):
```json
{
  "transaction": {
    "amount": 1000.50,
    "installments": 3,
    "requested_at": "2026-03-11T18:45:53Z"
  },
  "customer": {
    "avg_amount": 2000,
    "tx_count_24h": 5,
    "known_merchants": ["M1", "M2", "M3"]
  },
  "merchant": {
    "id": "M2",
    "mcc": "5411",
    "avg_amount": 500
  },
  "terminal": {
    "is_online": true,
    "card_present": false,
    "km_from_home": 50.5
  },
  "last_transaction": {
    "timestamp": "2026-03-11T18:30:00Z",
    "km_from_current": 10.2
  }
}
```

`last_transaction` may be `null`.

Response:
```json
{"approved": true, "fraud_score": 0.2}
```

### `GET /ready`

Response:
```json
{"ready": true, "build": "v12-opt"}
```

## Configuration

### Normalization (`resources/normalization.json`)

```json
{
  "max_amount": 10000,
  "max_installments": 12,
  "amount_vs_avg_ratio": 10,
  "max_minutes": 1440,
  "max_km": 1000,
  "max_tx_count_24h": 20,
  "max_merchant_avg_amount": 10000
}
```

### MCC Risk (`resources/mcc_risk.json`)

Maps 4-digit MCC codes to risk scores 0.0–1.0. Unknown codes default to 0.5.

## Testing

```sh
go test ./internal/...
```

## Docker

```sh
# Build
docker build -t fraud-detector .

# Run with compose
docker compose up
```

The compose setup runs:
- 1 LB instance (8 MB, 0.05 CPU) on port 9999
- 2 API server instances (170 MB, 0.475 CPU each) behind the LB

## Performance Optimizations

- **GC disabled** (`GOGC=-1`, `SetGCPercent(-1)`)
- **Memory locked** (`mlockall` with `MCL_CURRENT | MCL_FUTURE`)
- **Real-time scheduling** (`SCHED_FIFO` priority 10)
- **Epoll busy-polling** (50 µs budget) for sub-millisecond wakeups
- **Timer slack minimized** (`PR_SET_TIMERSLACK=1`)
- **TCP optimizations**: `TCP_NODELAY`, `TCP_QUICKACK`, `TCP_DEFER_ACCEPT`, `SO_BUSY_POLL`
- **Custom JSON parser** that bypasses `encoding/json`
- **Pre-built HTTP responses** as byte slices
- **`sync.Pool`** for buffer reuse
- **File descriptor passing** instead of connection proxying
