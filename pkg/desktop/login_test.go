package desktop

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetToken(t *testing.T) {
	valid := makeToken(t, time.Now().Add(time.Hour))
	expired := makeToken(t, time.Now().Add(-time.Hour))

	t.Run("valid token returned as-is", func(t *testing.T) {
		backend := &fakeBackend{token: valid}
		installFakeBackend(t, backend)

		assert.Equal(t, valid, GetToken(t.Context()))
		assert.Equal(t, 0, backend.refreshes())
	})

	t.Run("expired token triggers forced refresh", func(t *testing.T) {
		backend := &fakeBackend{token: expired}
		backend.onRefresh = func() { backend.setToken(valid) }
		installFakeBackend(t, backend)

		assert.Equal(t, valid, GetToken(t.Context()))
		assert.Equal(t, 1, backend.refreshes())
	})

	t.Run("stale token returned when refresh does not help", func(t *testing.T) {
		backend := &fakeBackend{token: expired}
		installFakeBackend(t, backend)

		assert.Equal(t, expired, GetToken(t.Context()))
		assert.Equal(t, 1, backend.refreshes())
	})

	t.Run("cooldown prevents repeated refresh nudges", func(t *testing.T) {
		backend := &fakeBackend{token: expired}
		installFakeBackend(t, backend)

		assert.Equal(t, expired, GetToken(t.Context()))
		assert.Equal(t, expired, GetToken(t.Context()))
		assert.Equal(t, 1, backend.refreshes())
	})

	t.Run("cooldown path picks up token refreshed by another caller", func(t *testing.T) {
		backend := &fakeBackend{token: expired}
		installFakeBackend(t, backend)

		assert.Equal(t, expired, GetToken(t.Context()))
		backend.setToken(valid)
		assert.Equal(t, valid, GetToken(t.Context()))
		assert.Equal(t, 1, backend.refreshes())
	})

	t.Run("non-JWT token returned as-is", func(t *testing.T) {
		backend := &fakeBackend{token: "not-a-jwt"}
		installFakeBackend(t, backend)

		assert.Equal(t, "not-a-jwt", GetToken(t.Context()))
		assert.Equal(t, 0, backend.refreshes())
	})

	t.Run("empty token returned as-is", func(t *testing.T) {
		backend := &fakeBackend{}
		installFakeBackend(t, backend)

		assert.Empty(t, GetToken(t.Context()))
		assert.Equal(t, 0, backend.refreshes())
	})
}

func TestTokenExpired(t *testing.T) {
	assert.False(t, tokenExpired(makeToken(t, time.Now().Add(time.Minute))))
	assert.True(t, tokenExpired(makeToken(t, time.Now().Add(-time.Minute))))
	assert.False(t, tokenExpired("not-a-jwt"))
}

func makeToken(t *testing.T, exp time.Time) string {
	t.Helper()
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"exp": exp.Unix()}).SignedString([]byte("secret"))
	require.NoError(t, err)
	return token
}

// fakeBackend emulates Docker Desktop's backend API: GET /registry/token
// serves the current token; POST /registry/credstore-updated triggers
// onRefresh (Desktop's async AutoLogin).
type fakeBackend struct {
	mu           sync.Mutex
	token        string
	refreshCalls int
	onRefresh    func()
}

func (b *fakeBackend) setToken(token string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.token = token
}

func (b *fakeBackend) refreshes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.refreshCalls
}

func (b *fakeBackend) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /registry/token", func(w http.ResponseWriter, _ *http.Request) {
		b.mu.Lock()
		token := b.token
		b.mu.Unlock()
		_ = json.NewEncoder(w).Encode(token)
	})
	mux.HandleFunc("POST /registry/credstore-updated", func(http.ResponseWriter, *http.Request) {
		b.mu.Lock()
		b.refreshCalls++
		onRefresh := b.onRefresh
		b.mu.Unlock()
		if onRefresh != nil {
			onRefresh()
		}
	})
	return mux
}

func installFakeBackend(t *testing.T, backend *fakeBackend) {
	t.Helper()

	ln := newMemListener()
	server := &http.Server{Handler: backend.handler()}
	go func() { _ = server.Serve(ln) }()
	t.Cleanup(func() {
		_ = server.Close()
		_ = ln.Close()
	})

	oldClient := ClientBackend
	ClientBackend = newRawClient(ln.dial)
	t.Cleanup(func() { ClientBackend = oldClient })

	oldCooldown, oldBudget, oldInterval := refreshCooldown, refreshPollBudget, refreshPollInterval
	refreshPollBudget = 100 * time.Millisecond
	refreshPollInterval = 5 * time.Millisecond
	t.Cleanup(func() {
		refreshCooldown, refreshPollBudget, refreshPollInterval = oldCooldown, oldBudget, oldInterval
	})

	func() {
		refreshState.Lock()
		defer refreshState.Unlock()
		refreshState.lastAttempt = time.Time{}
	}()
}

// memListener is an in-memory net.Listener fed by its dial method, so the
// RawClient can talk to a fake backend without a real socket.
type memListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newMemListener() *memListener {
	return &memListener{
		conns:  make(chan net.Conn),
		closed: make(chan struct{}),
	}
}

func (l *memListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *memListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *memListener) Addr() net.Addr {
	return &net.UnixAddr{Name: "mem", Net: "unix"}
}

func (l *memListener) dial(context.Context) (net.Conn, error) {
	clientSide, serverSide := net.Pipe()
	select {
	case l.conns <- serverSide:
		return clientSide, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}
