// Command loadgen pushes synthetic traffic at a healthMonitorNJ server and
// reports what arrived. It is the Phase 1 gate: the sustained rate has to be
// reached with zero measured loss, counting both what the kernel dropped and
// what the server could not place.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type options struct {
	addr     string
	status   string
	rate     int
	duration time.Duration
	senders  int
	urls     int
	users    int
	batch    int
	legacy   bool
	warmup   time.Duration
}

func main() {
	var o options
	flag.StringVar(&o.addr, "addr", "127.0.0.1:8082", "UDP address of the monitor")
	flag.StringVar(&o.status, "status", "http://127.0.0.1:8081", "HTTP base URL, for reading the counters back")
	flag.IntVar(&o.rate, "rate", 1000, "datagrams per second (0 = as fast as possible)")
	flag.DurationVar(&o.duration, "duration", 10*time.Second, "how long to send for")
	flag.IntVar(&o.senders, "senders", runtime.NumCPU(), "sending goroutines")
	flag.IntVar(&o.urls, "urls", 100, "distinct urls to spread over")
	flag.IntVar(&o.users, "users", 50, "distinct users to spread over")
	flag.IntVar(&o.batch, "batch", 1, "envelopes per datagram")
	flag.BoolVar(&o.legacy, "legacy", false, "send legacy protocol v0 packets instead of v1")
	flag.DurationVar(&o.warmup, "warmup", time.Second, "settle time before counting")
	flag.Parse()

	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, "loadgen:", err)
		os.Exit(1)
	}
}

func run(o options) error {
	before, haveBefore := readCounters(o.status)

	fmt.Printf("sending %s for %s at %s to %s\n",
		protocolName(o.legacy), o.duration, rateName(o.rate), o.addr)

	var sent, failed atomic.Int64
	stop := make(chan struct{})
	var wg sync.WaitGroup

	perSender := 0
	if o.rate > 0 {
		perSender = o.rate / max(o.senders, 1)
	}

	start := time.Now()
	for i := 0; i < o.senders; i++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			if err := sender(o, perSender, stop, &sent, &failed, seed); err != nil {
				fmt.Fprintln(os.Stderr, "sender:", err)
			}
		}(int64(i))
	}

	time.Sleep(o.duration)
	close(stop)
	wg.Wait()
	elapsed := time.Since(start)

	// The server flushes its readers every 250 ms and its writer every second.
	time.Sleep(2 * time.Second)

	fmt.Println()
	fmt.Printf("sent          %d datagrams (%d envelopes)\n", sent.Load(), sent.Load()*int64(o.batch))
	fmt.Printf("achieved      %.0f datagrams/s over %s\n", float64(sent.Load())/elapsed.Seconds(), elapsed.Round(time.Millisecond))
	if failed.Load() > 0 {
		fmt.Printf("send errors   %d\n", failed.Load())
	}

	after, haveAfter := readCounters(o.status)
	if !haveBefore || !haveAfter {
		fmt.Println("\nno counters available: pass -status to read them from the server")
		return nil
	}
	report(o, sent.Load(), before, after)
	return nil
}

func sender(o options, rate int, stop <-chan struct{}, sent, failed *atomic.Int64, seed int64) error {
	conn, err := net.Dial("udp", o.addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	rng := rand.New(rand.NewSource(time.Now().UnixNano() + seed*7919))
	buf := make([]byte, 0, 2048)

	// Pacing a million packets a second with one timer per packet does not
	// work: the timer itself becomes the bottleneck at about 20 k/s. Instead
	// each sender wakes on a coarse tick and sends its share for that tick.
	const tick = 2 * time.Millisecond
	perTick := 0.0
	credit := 0.0
	var ticker *time.Ticker
	if rate > 0 {
		// Fractional, not rounded up: at 1 000/s split over many senders a
		// sender owes less than one packet per tick, and rounding that up
		// would send an order of magnitude too much.
		perTick = float64(rate) * tick.Seconds()
		ticker = time.NewTicker(tick)
		defer ticker.Stop()
	}

	for {
		select {
		case <-stop:
			return nil
		default:
		}
		if ticker != nil {
			select {
			case <-stop:
				return nil
			case <-ticker.C:
			}
		}

		burst := 1
		if ticker != nil {
			credit += perTick
			burst = int(credit)
			credit -= float64(burst)
		}
		for i := 0; i < burst; i++ {
			buf = buf[:0]
			buf = appendDatagram(buf, o, rng)
			if _, err := conn.Write(buf); err != nil {
				failed.Add(1)
				continue
			}
			sent.Add(1)
		}
	}
}

func appendDatagram(buf []byte, o options, rng *rand.Rand) []byte {
	if o.batch > 1 {
		buf = append(buf, '[')
	}
	for i := 0; i < max(o.batch, 1); i++ {
		if i > 0 {
			buf = append(buf, ',')
		}
		buf = appendEnvelope(buf, o, rng)
	}
	if o.batch > 1 {
		buf = append(buf, ']')
	}
	return buf
}

func appendEnvelope(buf []byte, o options, rng *rand.Rand) []byte {
	url := fmt.Sprintf("/api/resource/%d", rng.Intn(max(o.urls, 1)))
	user := fmt.Sprintf("user%d", rng.Intn(max(o.users, 1)))
	ms := rng.Intn(400) + 5
	ts := time.Now().Format("2006-01-02T15:04:05.000-0700")

	if o.legacy {
		return fmt.Appendf(buf,
			`{"type":"request","start":"%s","executionTime":%d,"level":"DEBUG",`+
				`"serverName":"load-1","port":"8080","user":"%s","account":"42","accountName":"Acme",`+
				`"ipAddress":"203.0.113.%d","url":"%s"}`,
			ts, ms, user, rng.Intn(250)+1, url)
	}
	return fmt.Appendf(buf,
		`{"v":1,"t":"metric","signal":"requests","source":"load-1","ts":"%s",`+
			`"dims":{"url":"%s","user":"%s","account":"42","accountName":"Acme","serverName":"load-1"},`+
			`"values":{"count":1,"ms":%d}}`,
		ts, url, user, ms)
}

type counters struct {
	Intake struct {
		Decoded     int64 `json:"decoded"`
		Legacy      int64 `json:"legacy"`
		DecodeErrs  int64 `json:"decodeErrors"`
		Quarantined int64 `json:"quarantined"`
		TooOld      int64 `json:"tooOld"`
		TooNew      int64 `json:"tooNew"`
		NoAdapter   int64 `json:"noAdapter"`
		KernelDrops int64 `json:"kernelDrops"`
	} `json:"intake"`
	Writer struct {
		Written int64 `json:"written"`
		Dropped int64 `json:"dropped"`
		Errors  int64 `json:"errors"`
	} `json:"writer"`
}

func readCounters(base string) (counters, bool) {
	var c counters
	if base == "" {
		return c, false
	}
	res, err := http.Get(strings.TrimRight(base, "/") + "/api/state")
	if err != nil {
		return c, false
	}
	defer func() { _ = res.Body.Close() }()
	if err := json.NewDecoder(res.Body).Decode(&c); err != nil {
		return c, false
	}
	return c, true
}

func report(o options, sent int64, before, after counters) {
	envelopes := sent * int64(max(o.batch, 1))
	decoded := after.Intake.Decoded - before.Intake.Decoded
	drops := after.Intake.KernelDrops - before.Intake.KernelDrops
	rejected := (after.Intake.DecodeErrs - before.Intake.DecodeErrs) +
		(after.Intake.Quarantined - before.Intake.Quarantined) +
		(after.Intake.TooOld - before.Intake.TooOld) +
		(after.Intake.TooNew - before.Intake.TooNew) +
		(after.Intake.NoAdapter - before.Intake.NoAdapter)
	lost := envelopes - decoded

	fmt.Println()
	fmt.Printf("envelopes     %d sent, %d decoded\n", envelopes, decoded)
	fmt.Printf("kernel drops  %d\n", drops)
	fmt.Printf("rejected      %d (decode errors, unknown signals, too old, too new)\n", rejected)
	fmt.Printf("written       %d aggregates, %d dropped by the writer, %d errors\n",
		after.Writer.Written-before.Writer.Written,
		after.Writer.Dropped-before.Writer.Dropped,
		after.Writer.Errors-before.Writer.Errors)

	loss := 0.0
	if envelopes > 0 {
		loss = float64(lost) / float64(envelopes) * 100
	}
	fmt.Printf("\nloss          %d envelopes (%.4f%%)\n", lost, loss)
	if lost <= 0 && drops == 0 {
		fmt.Println("result        PASS — nothing lost")
		return
	}
	fmt.Println("result        FAIL — see the counters above")
	os.Exit(1)
}

func protocolName(legacy bool) string {
	if legacy {
		return "protocol v0 (legacy)"
	}
	return "protocol v1"
}

func rateName(rate int) string {
	if rate <= 0 {
		return "full speed"
	}
	return fmt.Sprintf("%d datagrams/s", rate)
}
