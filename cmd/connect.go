package cmd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/OpenSlides/openslides-performance/client"
	"github.com/spf13/cobra"
	"github.com/vbauerster/mpb/v7"
	"github.com/vbauerster/mpb/v7/decor"
)

const connectHelp = `Opens many connections to the autoupdate-service

Every connection is open and is waiting for messages. For each change
you see a progress bar that shows how many connections got an answer for
this change.`

func cmdConnect(cfg *config) *cobra.Command {
	cmd := cobra.Command{
		Use:   "connect",
		Short: "Opens many connections to autoupdate and keeps them open.",
		Long:  connectHelp,
	}

	connectionCount := cmd.Flags().IntP("number", "n", 10, "Number of users to use.")
	autoupdateBody := cmd.Flags().StringP(
		"body",
		"b",
		`[{"collection":"organization","ids":[1],"fields":{"committee_ids":{"type":"relation-list","collection":"committee","fields":{"name":null}}}}]`,
		"Amount of users to use.",
	)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		ctx, cancel := interruptContext()
		defer cancel()

		c, err := client.New(cfg.addr(), cfg.forceIPv4)
		if err != nil {
			return fmt.Errorf("creating client: %w", err)
		}

		if err := c.Login(ctx, cfg.username, cfg.password); err != nil {
			return fmt.Errorf("login client: %w", err)
		}

		progress := mpb.New()
		received := make(chan string, 1)

		for i := 0; i < *connectionCount; i++ {
			go func(i int) {
				var r io.ReadCloser
				for tries := 0; ; tries++ {
					if tries > 100 {
						return
					}

					r, err = keepOpen(ctx, c, "/system/autoupdate?compress=1", strings.NewReader(*autoupdateBody))
					if err != nil {
						if ctx.Err() != nil {
							return
						}

						log.Printf("Can not send request %d: %v", i, err)
						time.Sleep(time.Second)
						continue
					}
					break
				}
				defer r.Close()

				scanner := bufio.NewScanner(r)
				const MB = 1 << 20
				scanner.Buffer(make([]byte, 10), 16*MB)

				changeID := 0
				for scanner.Scan() {
					changeID++
					received <- fmt.Sprintf("Change %d", changeID)
				}

				if err := scanner.Err(); err != nil {
					if errors.Is(err, context.Canceled) {
						return
					}
					log.Printf("Can not read body: %v", err)
					return
				}
			}(i)
		}

		cidToBar := make(map[string]*mpb.Bar)

		for {
			select {
			case <-ctx.Done():
				return nil
			case msg := <-received:
				bar, ok := cidToBar[msg]
				if !ok {
					bar = progress.AddBar(
						int64(*connectionCount),
						mpb.PrependDecorators(decor.Name(msg)),
						mpb.AppendDecorators(decor.Elapsed(decor.ET_STYLE_GO)),
						mpb.AppendDecorators(decor.CountersNoUnit(" %d/%d")),
					)
					cidToBar[msg] = bar
				}
				bar.Increment()
			}
		}

	}

	return &cmd
}

func keepOpen(ctx context.Context, c *client.Client, path string, body io.Reader) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", path, body)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := c.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sending request to %s: %w", path, err)
	}
	return resp.Body, nil
}
