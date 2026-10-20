package api

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/pokemon/poracleng/processor/internal/humanlock"
)

func TestHumanLockMiddleware_SerialisesSameHuman(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(HumanLock())
	var inside, overlaps int32
	r.POST("/api/x/:id", func(c *gin.Context) {
		if atomic.AddInt32(&inside, 1) != 1 {
			atomic.AddInt32(&overlaps, 1)
		}
		time.Sleep(5 * time.Millisecond)
		atomic.AddInt32(&inside, -1)
		c.Status(http.StatusOK)
	})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/x/u1", nil))
			if w.Code != http.StatusOK {
				t.Errorf("code %d", w.Code)
			}
		}()
	}
	wg.Wait()
	if overlaps != 0 {
		t.Fatalf("%d overlapping handlers", overlaps)
	}
}

func TestHumanLockMiddleware_GetNotLocked(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(HumanLock())
	r.GET("/api/x/:id", func(c *gin.Context) { c.Status(http.StatusOK) })
	defer humanlock.Lock("u1")()
	done := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/x/u1", nil))
		done <- w.Code
	}()
	select {
	case code := <-done:
		if code != http.StatusOK {
			t.Fatalf("code %d", code)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("GET blocked on human lock")
	}
}
