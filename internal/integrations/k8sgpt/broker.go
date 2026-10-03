package k8sgpt

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"math/big"
	"net"
	"net/http"
	"net/url"
	kube "ops-platform/internal/integrations/kubernetes"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
)

// ReadBroker exists only during one Analyzer invocation inside the Worker.
// Its fixed GET routes use the collector's existing cluster rate budget and
// credential transport. The CLI receives only a private loopback credential.
type ReadBroker struct {
	Client         *kube.Client
	Namespace      string
	RequiredLabels map[string]string
	Token          string
	Authorize      func(context.Context) error
	mu             sync.Mutex
	objects        map[string]unstructured.Unstructured
	parents        map[string]brokerParent
	failures       []string
	completed      map[string]bool
}

func (b *ReadBroker) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	stage := "authorization"
	deny := func(status int) {
		b.mu.Lock()
		b.failures = append(b.failures, r.URL.Path+"/"+stage+"/"+http.StatusText(status))
		b.mu.Unlock()
		http.Error(w, http.StatusText(status), status)
	}
	credential := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if b.Client == nil || b.Authorize == nil || b.Token == "" || r.Method != "GET" || subtle.ConstantTimeCompare([]byte(credential), []byte(b.Token)) != 1 {
		deny(403)
		return
	}
	if b.serveOwner(w, r, deny) {
		return
	}
	path := r.URL.Path
	eventName := ""
	if path == "/api/v1/namespaces/"+b.Namespace+"/events" && len(r.URL.Query()) == 1 {
		selector := r.URL.Query().Get("fieldSelector")
		if strings.HasPrefix(selector, "involvedObject.name=") && !strings.ContainsAny(selector, ",()!") {
			eventName = strings.TrimPrefix(selector, "involvedObject.name=")
		}
	}
	allowed := path == "/version" || path == "/api/v1/nodes" || path == "/api/v1/namespaces/"+b.Namespace+"/pods" || path == "/api/v1/namespaces/"+b.Namespace+"/persistentvolumeclaims" || eventName != ""
	if !allowed || r.URL.RawPath != "" || (eventName == "" && len(r.URL.Query()) != 0) {
		deny(403)
		return
	}
	if eventName != "" {
		b.mu.Lock()
		found := false
		for _, o := range b.objects {
			if o.GetNamespace() == b.Namespace && o.GetName() == eventName {
				found = true
			}
		}
		b.mu.Unlock()
		if !found {
			deny(403)
			return
		}
	}
	if b.Authorize(r.Context()) != nil {
		deny(403)
		return
	}
	target := path
	if path != "/version" {
		keys := make([]string, 0, len(b.RequiredLabels))
		for key := range b.RequiredLabels {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		selectors := []string{}
		for _, key := range keys {
			selectors = append(selectors, key+"="+b.RequiredLabels[key])
		}
		query := url.Values{"limit": {"500"}}
		if eventName != "" {
			query.Set("fieldSelector", r.URL.Query().Get("fieldSelector"))
		} else if len(selectors) > 0 {
			query.Set("labelSelector", strings.Join(selectors, ","))
		}
		target += "?" + query.Encode()
	}
	upstream, err := b.Client.Do(r.Context(), "GET", target, nil)
	stage = "upstream-network"
	if err != nil {
		deny(503)
		return
	}
	defer upstream.Body.Close()
	stage = "upstream-status-" + http.StatusText(upstream.StatusCode)
	if upstream.StatusCode != 200 {
		deny(503)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(upstream.Body, (1<<20)+1))
	stage = "response-budget"
	if err != nil || len(raw) > 1<<20 {
		deny(503)
		return
	}
	if path == "/version" {
		stage = "version-format"
		var version struct {
			GitVersion string `json:"gitVersion"`
		}
		if json.Unmarshal(raw, &version) != nil || version.GitVersion == "" {
			deny(503)
			return
		}
	} else {
		stage = "list-format-or-pagination"
		var list struct {
			Metadata struct {
				Continue string `json:"continue"`
			} `json:"metadata"`
			Items []struct {
				Metadata struct {
					Namespace string            `json:"namespace"`
					UID       string            `json:"uid"`
					Labels    map[string]string `json:"labels"`
				} `json:"metadata"`
			} `json:"items"`
		}
		if json.Unmarshal(raw, &list) != nil || list.Items == nil || len(list.Items) > 500 || list.Metadata.Continue != "" {
			deny(503)
			return
		}
		for _, item := range list.Items {
			stage = "list-object-scope"
			expected := b.Namespace
			if path == "/api/v1/nodes" {
				expected = ""
			}
			if item.Metadata.Namespace != expected || item.Metadata.UID == "" {
				deny(503)
				return
			}
			for key, value := range b.RequiredLabels {
				stage = "list-object-label"
				if eventName != "" {
					break
				}
				if item.Metadata.Labels[key] != value {
					deny(503)
					return
				}
			}
		}
		var native struct {
			Items []map[string]any `json:"items"`
		}
		stage = "list-object-native-decode"
		if json.Unmarshal(raw, &native) != nil {
			deny(503)
			return
		}
		b.mu.Lock()
		if b.objects == nil {
			b.objects = map[string]unstructured.Unstructured{}
		}
		for _, object := range native.Items {
			o := unstructured.Unstructured{Object: object}
			stage = "event-native-identity"
			if eventName != "" {
				uid, _, _ := unstructured.NestedString(o.Object, "involvedObject", "uid")
				found := false
				for _, known := range b.objects {
					if string(known.GetUID()) == uid && known.GetName() == eventName && known.GetNamespace() == b.Namespace {
						found = true
					}
				}
				if !found {
					b.mu.Unlock()
					deny(503)
					return
				}
			} else {
				kind := "Pod"
				if path == "/api/v1/nodes" {
					kind = "Node"
				} else if strings.HasSuffix(path, "persistentvolumeclaims") {
					kind = "PersistentVolumeClaim"
				}
				o.SetKind(kind)
				b.objects[kind+"|"+o.GetNamespace()+"|"+o.GetName()] = o
			}
		}
		b.mu.Unlock()
	}
	if b.Authorize(r.Context()) != nil {
		deny(403)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	b.mu.Lock()
	if b.completed == nil {
		b.completed = map[string]bool{}
	}
	b.completed[path] = true
	b.mu.Unlock()
	w.Write(raw)
}

func (b *ReadBroker) Failures() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return slices.Clone(b.failures)
}

func (b *ReadBroker) Complete() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.failures) == 0 && b.completed["/version"] && b.completed["/api/v1/nodes"] && b.completed["/api/v1/namespaces/"+b.Namespace+"/pods"] && b.completed["/api/v1/namespaces/"+b.Namespace+"/persistentvolumeclaims"]
}

// Start generates an ephemeral TLS trust root and kubeconfig in a private
// directory. Neither upstream credentials nor a public listening socket exist.
func (b *ReadBroker) Start(ctx context.Context) (string, func(), error) {
	dir, err := os.MkdirTemp("", "sp05-analyzer-read-")
	if err != nil {
		return "", nil, err
	}
	fail := func(err error) (string, func(), error) { os.RemoveAll(dir); return "", nil, err }
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fail(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fail(err)
	}
	now := time.Now()
	template := x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "SP05 private Analyzer read broker"}, NotBefore: now.Add(-time.Minute), NotAfter: now.Add(5 * time.Minute), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return fail(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fail(err)
	}
	ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	cert, err := tls.X509KeyPair(ca, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private}))
	if err != nil {
		return fail(err)
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return fail(err)
	}
	b.Token = hex.EncodeToString(token)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fail(err)
	}
	config := map[string]any{"apiVersion": "v1", "kind": "Config", "current-context": "private", "clusters": []any{map[string]any{"name": "private", "cluster": map[string]any{"server": "https://" + listener.Addr().String(), "certificate-authority-data": base64.StdEncoding.EncodeToString(ca)}}}, "users": []any{map[string]any{"name": "private", "user": map[string]any{"token": b.Token}}}, "contexts": []any{map[string]any{"name": "private", "context": map[string]any{"cluster": "private", "user": "private", "namespace": b.Namespace}}}}
	raw, _ := json.Marshal(config)
	path := filepath.Join(dir, "kubeconfig.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		listener.Close()
		return fail(err)
	}
	server := &http.Server{Handler: b, ReadHeaderTimeout: 2 * time.Second, WriteTimeout: 20 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	done := make(chan struct{})
	go func() {
		defer close(done)
		server.Serve(tls.NewListener(listener, &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}))
	}()
	closeBroker := func() { server.Close(); <-done; os.RemoveAll(dir) }
	return path, closeBroker, nil
}

func (b *ReadBroker) Objects() []unstructured.Unstructured {
	b.mu.Lock()
	defer b.mu.Unlock()
	keys := make([]string, 0, len(b.objects))
	for key := range b.objects {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	out := make([]unstructured.Unstructured, 0, len(keys))
	for _, key := range keys {
		item := b.objects[key]
		out = append(out, *item.DeepCopy())
	}
	return out
}
