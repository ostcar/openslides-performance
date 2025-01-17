package client

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// Client holds the connection to the OpenSlides server.
type Client struct {
	addr       string
	httpClient *http.Client

	authCookie *http.Cookie
	authToken  string
	userID     int
}

// New initializes a new client.
func New(addr string, forceIPv4 bool) (*Client, error) {
	var dialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	if forceIPv4 {
		var zeroDialer net.Dialer
		dialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
			return zeroDialer.DialContext(ctx, "tcp4", addr)
		}
	}

	c := Client{
		addr: addr,
		httpClient: &http.Client{
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				DialContext:     dialContext,
			},
		},
	}

	return &c, nil
}

// Do is like http.Do but uses the credentials.
func (c *Client) Do(req *http.Request) (*http.Response, error) {
	req.Header.Set("authentication", c.authToken)
	req.Header.Add("cookie", c.authCookie.String())

	if req.Header.Get("content-type") == "" {
		req.Header.Set("content-type", "application/json")
	}

	if req.URL.Host == "" {
		base, err := url.Parse(c.addr)
		if err != nil {
			return nil, fmt.Errorf("parsing base url: %w", err)
		}

		req.URL = base.ResolveReference(req.URL)
	}

	return checkStatus(c.httpClient.Do(req))
}

// Login uses the username and password to login the client. Sets the returned
// cookie for later requests.
func (c *Client) Login(ctx context.Context, username, password string) error {
	url := c.addr + "/system/auth/login"
	payload := fmt.Sprintf(`{"username": "%s", "password": "%s"}`, username, password)

	req, err := http.NewRequestWithContext(ctx, "POST", url, strings.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := checkStatus(c.httpClient.Do(req))
	if err != nil {
		return fmt.Errorf("sending login request: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body)

	c.authToken = resp.Header.Get("authentication")
	for _, cookie := range resp.Cookies() {
		if cookie.Name == "refreshId" {
			c.authCookie = cookie
			break
		}
	}

	id, err := decodeUserID(c.authToken)
	if err != nil {
		return fmt.Errorf("decoding user id from auth token: %w", err)
	}

	c.userID = id
	return nil
}

// decodeUserID returns the user id from a jwt token.
//
// It does not validate the token.
func decodeUserID(token string) (int, error) {
	parts := strings.Split(token, ".")
	encoded, err := base64.RawStdEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, fmt.Errorf("decoding jtw token %q: %w", parts[1], err)
	}

	var data struct {
		UserID int `json:"userId"`
	}
	if err := json.Unmarshal(encoded, &data); err != nil {
		return 0, fmt.Errorf("decoding user_id: %w", err)
	}

	return data.UserID, nil
}

// UserID returns the userID of the client.
func (c *Client) UserID() int {
	return c.userID
}

// checkStatus is a helper that can be used around http.Do().
//
// It checks, that the returned status code in the 200er range.
func checkStatus(resp *http.Response, err error) (*http.Response, error) {
	if err != nil {
		return nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			body = []byte("[can not read body]")
		}
		resp.Body.Close()
		return nil, fmt.Errorf("got status %s: %s", resp.Status, body)
	}
	return resp, nil
}
