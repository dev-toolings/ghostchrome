package cli

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var (
	flagWaitPortHost    string
	flagWaitTimeout     int
	flagWaitURLStatus   int
	flagWaitURLInsecure bool
)

var waitPortCmd = &cobra.Command{
	Use:   "wait-port <port>",
	Short: "Block until a TCP port accepts a connection (dev server up)",
	Long: `Useful to chain after starting a dev server in the background:

  bun dev &
  ghostchrome wait-port 3000 --timeout 30 && \
    ghostchrome preview http://localhost:3000`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		port, err := strconv.Atoi(args[0])
		if err != nil || port <= 0 || port > 65535 {
			exitErr("wait-port", fmt.Errorf("invalid port %q", args[0]))
		}
		addr := net.JoinHostPort(flagWaitPortHost, args[0])

		deadline := time.Now().Add(time.Duration(flagWaitTimeout) * time.Second)
		start := time.Now()
		for {
			conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				type portResult struct {
					Action string `json:"action"`
					Addr   string `json:"addr"`
					TimeMs int64  `json:"time_ms"`
				}
				took := time.Since(start).Milliseconds()
				output(&portResult{Action: "wait-port", Addr: addr, TimeMs: took},
					fmt.Sprintf("OK [wait-port] %s reachable after %dms", addr, took))
				return
			}
			if time.Now().After(deadline) {
				fmt.Fprintf(os.Stderr, "FAIL [wait-port] %s unreachable after %ds\n", addr, flagWaitTimeout)
				os.Exit(1)
			}
			time.Sleep(200 * time.Millisecond)
		}
	},
}

var waitURLCmd = &cobra.Command{
	Use:   "wait-url <url>",
	Short: "Block until a URL returns the expected HTTP status",
	Long: `Polls the URL via a direct HTTP request (no browser) and succeeds when
the response matches --status. Redirects are followed. Use --insecure to skip
TLS verification (self-signed certs in dev).

Examples:
  ghostchrome wait-url http://localhost:3000 --status 200
  ghostchrome wait-url http://localhost:3000/healthz --timeout 60`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		target := args[0]

		transport := &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: flagWaitURLInsecure}, //nolint:gosec
		}
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}

		deadline := time.Now().Add(time.Duration(flagWaitTimeout) * time.Second)
		start := time.Now()
		var lastErr string
		for {
			req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
			if err != nil {
				fmt.Fprintf(os.Stderr, "FAIL [wait-url] %s: invalid request: %v\n", target, err)
				os.Exit(1)
			}
			resp, err := client.Do(req)
			if err == nil && resp != nil {
				statusMatches := resp.StatusCode == flagWaitURLStatus
				resp.Body.Close()
				if statusMatches {
					took := time.Since(start).Milliseconds()
					type urlResult struct {
						Action string `json:"action"`
						URL    string `json:"url"`
						Status int    `json:"status"`
						TimeMs int64  `json:"time_ms"`
					}
					output(&urlResult{Action: "wait-url", URL: target, Status: resp.StatusCode, TimeMs: took},
						fmt.Sprintf("OK [wait-url] %s → %d after %dms", target, resp.StatusCode, took))
					return
				}
				lastErr = fmt.Sprintf("got %d, want %d", resp.StatusCode, flagWaitURLStatus)
			} else {
				lastErr = err.Error()
				// Shorten common connection refused errors for readability.
				if strings.Contains(lastErr, "connection refused") {
					lastErr = "connection refused"
				}
			}
			if time.Now().After(deadline) {
				fmt.Fprintf(os.Stderr, "FAIL [wait-url] %s (%s) after %ds\n", target, lastErr, flagWaitTimeout)
				os.Exit(1)
			}
			time.Sleep(300 * time.Millisecond)
		}
	},
}

func init() {
	waitPortCmd.Flags().StringVar(&flagWaitPortHost, "host", "127.0.0.1", "Host or IP to connect to")
	waitPortCmd.Flags().IntVar(&flagWaitTimeout, "timeout", 30, "Timeout in seconds")

	waitURLCmd.Flags().IntVar(&flagWaitTimeout, "timeout", 30, "Timeout in seconds")
	waitURLCmd.Flags().IntVar(&flagWaitURLStatus, "status", 200, "Expected HTTP status code")
	waitURLCmd.Flags().BoolVar(&flagWaitURLInsecure, "insecure", false, "Skip TLS certificate verification")

	rootCmd.AddCommand(waitPortCmd, waitURLCmd)
}
