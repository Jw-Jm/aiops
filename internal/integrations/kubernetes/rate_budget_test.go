package kubernetes

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestLeaseRequestsRemainBoundedDuringCollectionQueue(t *testing.T) {
	var count atomic.Int64
	var mu sync.Mutex
	var arrivals []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		count.Add(1)
		w.WriteHeader(200)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, server.Client(), 20, 50)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := client.Do(ctx, "GET", "/api/v1/pods", nil)
			if err != nil {
				t.Error(err)
				return
			}
			res.Body.Close()
		}()
	}
	for count.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	// The real HTTP queue is saturated before the GET/CAS control requests.
	time.Sleep(30 * time.Millisecond)
	leaseCtx, stop := context.WithTimeout(ctx, 700*time.Millisecond)
	defer stop()
	for _, method := range []string{"GET", "PUT"} {
		res, err := client.Do(leaseCtx, method, "/apis/coordination.k8s.io/v1/namespaces/owned/leases/graph", nil)
		if err != nil {
			t.Errorf("Lease %s starved behind collection: %v", method, err)
			break
		}
		res.Body.Close()
	}
	wg.Wait()
	if count.Load() != 62 {
		t.Errorf("requests did not all complete: %d/62", count.Load())
	}
	mu.Lock()
	defer mu.Unlock()
	sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].Before(arrivals[j]) })
	for i := 1; i < len(arrivals); i++ {
		if arrivals[i].Sub(arrivals[i-1]) < 40*time.Millisecond {
			t.Fatal("priority bypassed shared QPS pacing")
		}
	}
}

func TestTwoCredentialsShareActualRequestBudgetWithoutStarvation(t *testing.T) {
	var mu sync.Mutex
	var arrivals []time.Time
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer server.Close()
	a, _ := NewClient(server.URL, server.Client(), 20, 50)
	b, _ := NewClient(server.URL, server.Client(), 20, 50)
	if err := b.ShareRateBudget(a.budget); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			client := a
			if i%2 != 0 {
				client = b
			}
			res, err := client.Do(ctx, "GET", "/", nil)
			if err != nil {
				t.Error(err)
				return
			}
			io.Copy(io.Discard, res.Body)
			res.Body.Close()
		}(i)
	}
	wg.Wait()
	if len(arrivals) != 40 {
		t.Fatalf("starved callers: %d/40", len(arrivals))
	}
	sort.Slice(arrivals, func(i, j int) bool { return arrivals[i].Before(arrivals[j]) })
	elapsed := arrivals[39].Sub(arrivals[0])
	// Observe real HTTP arrivals, allowing 10ms local transport scheduling jitter.
	if elapsed < 1950*time.Millisecond-10*time.Millisecond {
		t.Fatalf("aggregate QPS budget bypassed: 40 requests in %s", elapsed)
	}
	t.Logf("two concurrent credential clients: actual HTTP requests=40 duration=%s aggregate intervals QPS=%f burst=1", elapsed, 39/elapsed.Seconds())
}

func TestContinuousLeaseTrafficDoesNotStarveCollection(t *testing.T) {
	var collection atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/pods" {
			collection.Add(1)
		}
		w.WriteHeader(200)
	}))
	defer server.Close()
	client, _ := NewClient(server.URL, server.Client(), 20, 50)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := client.Do(ctx, "GET", "/apis/coordination.k8s.io/v1/namespaces/owned/leases/graph", nil)
			if err != nil {
				t.Error(err)
				return
			}
			res.Body.Close()
		}()
	}
	// Repeated bulk requests must make progress while the control queue remains
	// saturated, and the whole queue must drain within the same total QPS bound.
	for i := 0; i < 10; i++ {
		bounded, stop := context.WithTimeout(ctx, 400*time.Millisecond)
		res, err := client.Do(bounded, "GET", "/api/v1/pods", nil)
		stop()
		if err != nil {
			t.Errorf("collection starved by control traffic: %v", err)
			break
		}
		res.Body.Close()
	}
	wg.Wait()
	if collection.Load() != 10 {
		t.Errorf("collection progress %d/10", collection.Load())
	}
}

func TestCanceledBudgetWaitersDoNotDelayNextLiveRequest(t *testing.T) {
	b, _ := NewRateBudget(20, 50)
	if err := b.wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
			defer cancel()
			if err := b.wait(ctx); err == nil {
				t.Error("canceled request reserved a slot")
			}
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := b.wait(ctx); err != nil {
		t.Fatalf("canceled callers left phantom reservations: %v", err)
	}
}
