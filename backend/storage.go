package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// evidenceBucket is the private Storage bucket holding all uploaded evidence
// files. The bucket is created at startup (when the service key is present)
// and is non-public: files are only ever retrieved through short-lived signed
// URLs issued by GET /api/files/{version_id}/download-url.
const evidenceBucket = "evidence-files"

// exportBucket holds chain-of-custody download packages (evidence ZIP +
// certificate). It is kept separate from evidenceBucket so the evidence bucket
// keeps its strict MIME allowlist while zip packages land in their own bucket.
const exportBucket = "qdesk-exports"

// storageClient is a thin HTTP client for the Supabase Storage API. It talks
// to the authenticated (service-role) endpoints only; no public anon access is
// used anywhere in the upload flow.
type storageClient struct {
	baseURL     string
	serviceKey  string
	maxUploadMB int64
	hc          *http.Client
}

// newStorageClient builds the Storage client. A missing service key is not an
// error here: upload/download handlers degrade to 503 until SUPABASE_SERVICE_
// ROLE_KEY is provisioned, and the failure is surfaced at request time.
func newStorageClient(baseURL, serviceKey string, maxUploadMB int64) *storageClient {
	if serviceKey == "" {
		log.Printf("storage: SUPABASE_SERVICE_ROLE_KEY is unset — evidence upload/download will answer 503")
	}
	return &storageClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		serviceKey:  serviceKey,
		maxUploadMB: maxUploadMB,
		hc:          &http.Client{Timeout: 120 * time.Second},
	}
}

func (sc *storageClient) authHeaders() http.Header {
	h := make(http.Header)
	h.Set("apikey", sc.serviceKey)
	h.Set("Authorization", "Bearer "+sc.serviceKey)
	h.Set("Content-Type", "application/json")
	return h
}

// ready reports whether uploads can be served (service key provisioned).
func (sc *storageClient) ready() bool {
	return sc.serviceKey != ""
}

// maxBytes returns the configured upload size cap in bytes.
func (sc *storageClient) maxBytes() int64 {
	return sc.maxUploadMB * 1024 * 1024
}

// ensureBucket creates the private buckets if they do not exist: evidenceBucket
// restricted to the evidence upload MIME allowlist, and exportBucket restricted
// to zip packages. The file_size_limit and allowed_mime_types mirror the
// API-level policy so the storage layer enforces the same bounds.
func (sc *storageClient) ensureBucket(ctx context.Context) error {
	for _, b := range []struct {
		name string
		mime []string
	}{
		{evidenceBucket, allowedUploadTypes()},
		{exportBucket, []string{"application/zip"}},
	} {
		if err := sc.ensureOneBucket(ctx, b.name, b.mime); err != nil {
			return err
		}
	}
	return nil
}

func (sc *storageClient) ensureOneBucket(ctx context.Context, name string, mimeTypes []string) error {
	if !sc.ready() {
		return nil
	}
	body, _ := json.Marshal(map[string]any{
		"name":               name,
		"public":             false,
		"file_size_limit":    sc.maxBytes(),
		"allowed_mime_types": mimeTypes,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sc.baseURL+"/storage/v1/bucket", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header = sc.authHeaders()
	resp, err := sc.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		log.Printf("storage: created private bucket %q", name)
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	// Supabase answers 400 "already exists" (or "Duplicate bucket") when the
	// bucket exists. Anything else is a real failure and must NOT be treated as
	// success — e.g. a 400 "exceeded the maximum allowed size" when
	// file_size_limit is above the plan's object cap.
	if resp.StatusCode == http.StatusBadRequest {
		lower := strings.ToLower(string(msg))
		if strings.Contains(lower, "already") || strings.Contains(lower, "duplicate") ||
			strings.Contains(lower, "exists") {
			return nil
		}
	}
	return fmt.Errorf("storage: create bucket returned %d: %s", resp.StatusCode, strings.TrimSpace(string(msg)))
}

// uploadObject stores the bytes under key in the given private bucket.
// The x-upsert header makes a retry after a mid-upload failure idempotent.
func (sc *storageClient) uploadObject(ctx context.Context, bucket, key, contentType string, r io.Reader) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut,
		sc.baseURL+"/storage/v1/object/"+storageObjectPath(bucket, key), r)
	if err != nil {
		return err
	}
	req.Header = sc.authHeaders()
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("x-upsert", "true")
	resp, err := sc.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("storage: upload failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(msg)))
}

// signedURL returns a short-lived, pre-signed URL for an object key. When
// download is true the token instructs the browser to save the object instead
// of rendering it. The token expires after expiresIn seconds — never persist or
// redirect to it longer than that. Typical usage is 60s so the browser can
// start the stream.
//
// Recent Supabase Storage versions no longer accept the old
// POST /object/sign/{bucket}/{key} + {"expiresIn"} form and respond with 400
// "body must have required property 'paths'". The bulk endpoint
// POST /object/sign/{bucket} + {"paths":[...]} is answered by both new and
// compatible hosts, so it is used unconditionally.
func (sc *storageClient) signedURL(ctx context.Context, bucket, key string, expiresIn int, download bool) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"paths":     []string{key},
		"expiresIn": expiresIn,
		"download":  download,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		sc.baseURL+"/storage/v1/object/sign/"+url.PathEscape(bucket), bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header = sc.authHeaders()
	resp, err := sc.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("storage: sign failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	var out []struct {
		Path      string `json:"path"`
		SignedURL string `json:"signedURL"`
		Error     string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil || len(out) == 0 || out[0].SignedURL == "" {
		return "", fmt.Errorf("storage: malformed sign response")
	}
	if out[0].Error != "" {
		return "", fmt.Errorf("storage: sign error: %s", out[0].Error)
	}
	// The sign response returns a path like "/object/sign/{bucket}/{key}?token=…".
	// Signed-object access is served under the /storage/v1 prefix (bare
	// /object/sign/… answers 404 "requested path is invalid").
	return sc.baseURL + "/storage/v1" + out[0].SignedURL, nil
}

// openObject GETs an object from the private bucket using the service key and
// returns the raw *http.Response for the caller to stream (Range requests pass
// through untouched). The caller MUST close resp.Body. Non-2xx responses are
// read into an error.
func (sc *storageClient) openObject(ctx context.Context, key, rangeHeader string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		sc.baseURL+"/storage/v1/object/authenticated/"+storageObjectPath(evidenceBucket, key), nil)
	if err != nil {
		return nil, err
	}
	req.Header = sc.authHeaders()
	if rangeHeader != "" {
		req.Header.Set("Range", rangeHeader)
	}
	resp, err := sc.hc.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return resp, nil
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return nil, fmt.Errorf("storage: object read failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(msg)))
}

// deleteObject best-effort removes an object (used to roll back a storage
// write when the DB insert fails afterwards).
func (sc *storageClient) deleteObject(ctx context.Context, key string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		sc.baseURL+"/storage/v1/object/"+storageObjectPath(evidenceBucket, key), nil)
	if err != nil {
		return
	}
	req.Header = sc.authHeaders()
	resp, err := sc.hc.Do(req)
	if err != nil {
		return
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

// storageObjectPath builds the URL path for a bucket+key pair, escaping each
// key segment so object keys (which may contain slashes) keep their structure.
func storageObjectPath(bucket, key string) string {
	segments := append([]string{bucket}, splitStorageKey(key)...)
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return strings.Join(segments, "/")
}
