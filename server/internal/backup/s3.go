package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"moesekai/server/internal/config"
	"moesekai/server/internal/importer"
)

// s3Settings is a snapshot of the S3 target config.
type s3Settings struct {
	endpoint  string // e.g. https://s3.amazonaws.com or https://<account>.r2.cloudflarestorage.com
	region    string
	bucket    string
	prefix    string
	accessKey string
	secretKey string
}

func (m *Manager) s3Config() (s3Settings, error) {
	s := s3Settings{
		endpoint:  strings.TrimRight(m.cfg.GetOr(config.KeyBackupS3Endpoint, "https://s3.amazonaws.com"), "/"),
		region:    m.cfg.GetOr(config.KeyBackupS3Region, "us-east-1"),
		bucket:    m.cfg.Get(config.KeyBackupS3Bucket),
		prefix:    strings.Trim(m.cfg.GetOr(config.KeyBackupS3Prefix, "moesekai-backups"), "/"),
		accessKey: m.cfg.Get(config.KeyBackupS3AccessKey),
		secretKey: m.cfg.Get(config.KeyBackupS3SecretKey),
	}
	if s.bucket == "" || s.accessKey == "" || s.secretKey == "" {
		return s, fmt.Errorf("s3 backup not fully configured (bucket/accessKey/secretKey required)")
	}
	return s, nil
}

func (m *Manager) backupS3() error {
	cfg, err := m.s3Config()
	if err != nil {
		return err
	}
	work := filepath.Join(m.workDir, "s3-backup")
	_ = os.RemoveAll(work)
	defer os.RemoveAll(work)
	srcDir, err := m.materializeTranslations(work)
	if err != nil {
		return err
	}
	tarball, err := tarGzDir(srcDir)
	if err != nil {
		return err
	}
	ts := time.Now().UTC().Format("20060102-150405")
	key := fmt.Sprintf("%s/translations-%s.tar.gz", cfg.prefix, ts)
	if err := m.s3Put(cfg, key, tarball); err != nil {
		return err
	}
	// Also write/overwrite a "latest" pointer object for easy restore.
	latestKey := fmt.Sprintf("%s/latest.tar.gz", cfg.prefix)
	return m.s3Put(cfg, latestKey, tarball)
}

func (m *Manager) restoreS3() (importer.Result, error) {
	cfg, err := m.s3Config()
	if err != nil {
		return importer.Result{}, err
	}
	latestKey := fmt.Sprintf("%s/latest.tar.gz", cfg.prefix)
	data, err := m.s3Get(cfg, latestKey)
	if err != nil {
		return importer.Result{}, err
	}
	work := filepath.Join(m.workDir, "s3-restore")
	_ = os.RemoveAll(work)
	if err := os.MkdirAll(work, 0o755); err != nil {
		return importer.Result{}, err
	}
	defer os.RemoveAll(work)
	if err := untarGz(data, work); err != nil {
		return importer.Result{}, err
	}
	// The tarball root is the translations dir contents.
	src := work
	if err := importer.ValidateDir(src); err != nil {
		// Maybe nested under translations/.
		nested := filepath.Join(work, "translations")
		if e2 := importer.ValidateDir(nested); e2 == nil {
			src = nested
		} else {
			return importer.Result{}, err
		}
	}
	return importer.ImportDir(src, m.store, m.eventStr)
}

// ---- tar.gz helpers ----

func tarGzDir(dir string) ([]byte, error) {
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = filepath.ToSlash(rel)
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if err != nil {
		return nil, err
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func untarGz(data []byte, dest string) error {
	gr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer gr.Close()
	tr := tar.NewReader(gr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		// Guard against path traversal in archive entries.
		target := filepath.Join(dest, filepath.Clean("/"+hdr.Name))
		if !strings.HasPrefix(target, filepath.Clean(dest)+string(os.PathSeparator)) && target != dest {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			f, err := os.Create(target)
			if err != nil {
				return err
			}
			if _, err := io.Copy(f, tr); err != nil {
				f.Close()
				return err
			}
			f.Close()
		}
	}
	return nil
}

// ---- minimal S3 SigV4 (PUT/GET single object) ----

func (m *Manager) s3Put(cfg s3Settings, key string, body []byte) error {
	return m.s3Do(cfg, http.MethodPut, key, body)
}

func (m *Manager) s3Get(cfg s3Settings, key string) ([]byte, error) {
	return m.s3DoResp(cfg, http.MethodGet, key, nil)
}

func (m *Manager) s3Do(cfg s3Settings, method, key string, body []byte) error {
	_, err := m.s3DoResp(cfg, method, key, body)
	return err
}

// s3DoResp performs a SigV4-signed request to <endpoint>/<bucket>/<key> (path
// style, which works for AWS S3, Cloudflare R2, MinIO, and most S3-compatibles).
func (m *Manager) s3DoResp(cfg s3Settings, method, key string, body []byte) ([]byte, error) {
	url := fmt.Sprintf("%s/%s/%s", cfg.endpoint, cfg.bucket, key)
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	payloadHash := sha256Hex(body)

	host := req.URL.Host
	req.Header.Set("Host", host)
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/gzip")
	}

	canonicalURI := req.URL.EscapedPath()
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalHeaders := fmt.Sprintf("host:%s\nx-amz-content-sha256:%s\nx-amz-date:%s\n", host, payloadHash, amzDate)
	canonicalRequest := strings.Join([]string{
		method, canonicalURI, "", canonicalHeaders, signedHeaders, payloadHash,
	}, "\n")

	scope := fmt.Sprintf("%s/%s/s3/aws4_request", dateStamp, cfg.region)
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", amzDate, scope, sha256Hex([]byte(canonicalRequest)),
	}, "\n")

	signingKey := sigv4Key(cfg.secretKey, dateStamp, cfg.region, "s3")
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	auth := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		cfg.accessKey, scope, signedHeaders, signature)
	req.Header.Set("Authorization", auth)

	client := &http.Client{Timeout: 2 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("s3 %s %s: http %d: %s", method, key, resp.StatusCode, s3ErrMsg(respBody))
	}
	return respBody, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sigv4Key(secret, dateStamp, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), dateStamp)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, service)
	return hmacSHA256(kService, "aws4_request")
}

func s3ErrMsg(body []byte) string {
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if xml.Unmarshal(body, &e) == nil && e.Code != "" {
		return e.Code + ": " + e.Message
	}
	msg := strings.TrimSpace(string(body))
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}
