# Q-DESK

A secure desktop application built with **Tauri v2 + Next.js 14 + Go** monorepo architecture.

## Folder Structure

```
q-desk/
├── backend/                # Go 1.23+ API service
│   └── go.mod
├── src-tauri/              # Tauri v2 Rust shell
│   ├── src/
│   ├── Cargo.toml
│   └── tauri.conf.json
├── src/                    # Next.js 14 App Router
│   └── app/
├── db/
│   └── migrations/         # schema.sql lives here
├── .env.example
├── .env
├── .gitignore
└── README.md
```

## Core Features

1. **PQC Handshake** - Post-quantum cryptographic key exchange for secure client-server communication
2. **Ticket-Based Access** - Time-limited access tickets for authentication and authorization
3. **10-Minute Expiry** - Automatic ticket expiration (configurable via `TICKET_TTL_SECONDS`)
4. **Hash-Chained Version Control** - Immutable, tamper-evident version history using hash chains
5. **Phone Detection** - Device fingerprinting for multi-device awareness
6. **Evidence File Upload** - Magic-byte-vetted evidence ingest into a private Storage bucket, hash-chained version rows, and short-lived signed download URLs

## Setup Instructions

### Prerequisites

- Node.js 18+
- Go 1.23+
- Rust toolchain (for Tauri)
- pnpm or npm

### Installation

```bash
# Clone the repository
git clone <repo-url>
cd q-desk

# Install frontend dependencies
cd src
npm install

# Copy environment file
cp ../.env.example ../.env

# Edit .env with your actual values
# (Never commit real secrets to version control)
```

### Database Setup

```bash
# Run migrations from db/migrations/
psql -U your_user -d your_db -f db/migrations/schema.sql
```

### Running the Application

```bash
# Start the Go backend
cd backend
go run .

# Start the Next.js frontend (in another terminal)
cd src
npm run dev

# Start Tauri dev (with both frontend and backend running)
cd src-tauri
cargo tauri dev
```

## Environment Variables

| Variable | Description |
|----------|-------------|
| `SUPABASE_DATABASE_URL` | Database connection string |
| `UPSTASH_REDIS_ADDR` | Redis server address |
| `UPSTASH_REDIS_PASSWORD` | Redis authentication password |
| `MLDSA_SERVER_PRIVATE_KEY` | ML-DSA private key for PQC signing |
| `MLDSA_SERVER_PUBLIC_KEY` | ML-DSA public key for verification |
| `TICKET_TTL_SECONDS` | Ticket time-to-live in seconds (default: 600) |
| `SUPABASE_URL` | Storage REST URL (defaults to the project inferred from `SUPABASE_DATABASE_URL`) |
| `SUPABASE_SERVICE_ROLE_KEY` | Service role key for the private `evidence-files` bucket (required for uploads) |
| `MAX_UPLOAD_SIZE_MB` | Evidence upload cap in MB (default: 100) |
| `MALWARE_SCAN_ENABLED` | `false` = prototype mode (upload not scanned, audited as `skipped_prototype_mode`) |
| `NEXT_PUBLIC_WS_URL` | WebSocket URL for real-time communication |

## Evidence Uploads & Malware Scanning (TODO)

Evidence files are stored in a **private** Supabase Storage bucket (`evidence-files`) and
are only ever retrieved through **60-second pre-signed URLs** issued per view-scope
ticket — never public or permanent links.

- Uploads are validated by **magic bytes** (JPEG, PNG, PDF, MP4, MP3, WAV); the
  client-declared Content-Type is never trusted.
- The server computes the SHA-256 of the raw bytes, stores them in storage, and only
  then writes the hash-chained `document_versions` row (same chain formula as
  `/api/append`), so existing chain verification keeps working for uploads.
- **Malware scanning is prototype-mode by default** (`MALWARE_SCAN_ENABLED=false`):
  uploads are NOT scanned. Each upload logs a server warning and records
  `scan_status: "skipped_prototype_mode"` on the hash-chained `UPLOAD` audit row so
  the gap is never silently hidden.
- **TODO (pre-production):** wire a real scanner (e.g. ClamAV `clamscan` on a staged
  temp file) into `backend/upload.go` behind `malwareScanner` / `MALWARE_SCAN_ENABLED`.
  The flag already **fails closed** — enabling it without a scanner backend rejects
  uploads (503) rather than passing them through unscanned. Set the bucket
  `allowed_mime_types` to match when you tighten the allowlist.

### Storage setup (one-time)

1. Add `SUPABASE_SERVICE_ROLE_KEY` (+ optionally `SUPABASE_URL`) to `.env`.
2. Restart the backend — it creates the private `evidence-files` bucket on startup.
3. In the Supabase dashboard open **Storage → Rules** for `evidence-files` and add CORS
   for the webview origins: `http://localhost:3000`, `http://tauri.localhost`,
   `tauri://localhost`.

## License

Private - All rights reserved.
