package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	// "golang.org/x/sys/windows"
)

var punchInterval = 500 * time.Millisecond

func punchHoles(conn *net.UDPConn, remoteAddr *net.UDPAddr, done chan struct{}) {
	ticker := time.NewTicker(punchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			_, err := conn.WriteToUDP([]byte("ping"), remoteAddr)
			if err != nil {
				// Silently continue on ping errors
				continue
			}
		}
	}
}

// func pollConsoleSize(p *tea.Program) {
// 	var lastCols, lastRows int
// 	for {
// 		cols, rows := getConsoleSize()
// 		if cols != lastCols || rows != lastRows {
// 			p.Send(ResizeMsg{
// 				cols: cols,
// 				rows: rows,
// 			})
// 			lastCols = cols
// 			lastRows = rows
// 		}
// 		time.Sleep(200 * time.Millisecond) // maybe adjust polling interval
// 	}
// }

// func getConsoleSize() (cols, rows int) {
// 	var info windows.ConsoleScreenBufferInfo
// 	handle := windows.Handle(os.Stdout.Fd())
// 	err := windows.GetConsoleScreenBufferInfo(handle, &info)
// 	if err == nil {
// 		cols = int(info.Size.X)
// 		rows = int(info.Size.Y)
// 	}
// 	return
// }

type Message struct {
	time      time.Time
	ip        string
	port      int
	text      string
	delivered bool
}

type (
	Response Message
	Ping     Message
)

type Model struct {
	mu   sync.Mutex    // Protects concurrent access to messages
	done chan struct{} // Signals shutdown to background goroutines

	sub          chan Response // Channel for receiving message notifications
	pingSub      chan Ping
	lastPingTime *time.Time

	conn          *net.UDPConn
	remoteAddr    *net.UDPAddr
	localPort     int
	discoveryAddr *net.UDPAddr

	peerMessages []Message
	userMessages []Message
	allMessages  []Message

	hoveredMessageIndex int
	hoveredMessage      string
	copied              bool

	textInput textinput.Model

	// rows int
	// cols int
}

// type ResizeMsg struct {
// 	rows int
// 	cols int
// }

var (
	bubblePinkAccentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	buttonStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#00ff00"))
)

// A command to send a message to the remote peer
func sendMessage(conn *net.UDPConn, remoteAddr *net.UDPAddr, message string) tea.Cmd {
	return func() tea.Msg {
		_, _ = conn.WriteToUDP([]byte(message), remoteAddr)
		return nil
	}
}

// A command to listen for messages on our local port
func listenForMessages(sub chan<- Response, pingSub chan<- Ping, conn *net.UDPConn, done <-chan struct{}) tea.Cmd {
	return func() tea.Msg {
		buffer := make([]byte, 1024)
		for {
			select {
			case <-done:
				// stop listening
				return nil
			default:
				conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
				n, addr, err := conn.ReadFromUDP(buffer)
				if err != nil {
					// try again
					continue
				}

				if string(buffer[:n]) == "ping" {
					pingSub <- Ping(Message{
						time: time.Now(),
						ip:   addr.IP.String(),
						port: addr.Port,
						text: string(buffer[:n]),
					})
				} else {
					sub <- Response(Message{
						time: time.Now(),
						ip:   addr.IP.String(),
						port: addr.Port,
						text: string(buffer[:n]),
					})
				}
			}
		}
	}
}

// A command that waits for messages on a channel.
func waitForMessages(sub <-chan Response) tea.Cmd {
	return func() tea.Msg {
		return <-sub
	}
}

// A command that waits for pings on a channel.
func waitForPings(sub <-chan Ping) tea.Cmd {
	return func() tea.Msg {
		return <-sub
	}
}

// A command to request the discovery server for our external address
func requestAddress(conn *net.UDPConn, discoveryAddr *net.UDPAddr) tea.Cmd {
	_, _ = conn.WriteToUDP([]byte("whoami"), discoveryAddr)
	return nil
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		listenForMessages(m.sub, m.pingSub, m.conn, m.done),
		waitForMessages(m.sub),
		waitForPings(m.pingSub),
	)
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyDown:
			if len(m.allMessages) > 0 {
				m.hoveredMessageIndex = clamp(m.hoveredMessageIndex+1, 0, len(m.allMessages))
				m.copied = false
			}
			if m.hoveredMessageIndex < len(m.allMessages) {
				m.hoveredMessage = m.allMessages[m.hoveredMessageIndex].text
			} else {
				m.hoveredMessage = ""
			}
			return m, nil

		case tea.KeyUp:
			if len(m.allMessages) > 0 {
				m.hoveredMessageIndex = clamp(m.hoveredMessageIndex-1, 0, len(m.allMessages))
				m.copied = false
			}
			if m.hoveredMessageIndex < len(m.allMessages) {
				m.hoveredMessage = m.allMessages[m.hoveredMessageIndex].text
			} else {
				m.hoveredMessage = ""
			}
			return m, nil

		case tea.KeyEnter:
			// enter only copies to clipboard
			if m.hoveredMessageIndex < len(m.allMessages) && len(m.allMessages) > 0 {
				_ = clipboard.WriteAll(m.hoveredMessage)
				m.copied = true
				return m, nil
			}

			// enter does nothing
			input := m.textInput.Value()
			if input == "" {
				return m, nil
			}
			// enter quits application
			if input == "/quit" {
				close(m.done)
				return m, tea.Quit
			}

			switch input {
			// enter gets our external address
			case "/getaddr":
				m.textInput.Reset()
				return m, requestAddress(m.conn, m.discoveryAddr)
				// enter sends message
			default:
				m.hoveredMessageIndex++
				m.copied = false
				m.textInput.Reset()

				var delivered bool
				if m.lastPingTime != nil {
					delivered = time.Since(*m.lastPingTime) <= punchInterval
				}

				m.mu.Lock()
				m.userMessages = append(m.userMessages, Message{
					time:      time.Now(),
					ip:        bubblePinkAccentStyle.Render("(You)") + " localhost",
					port:      m.localPort,
					text:      input,
					delivered: delivered,
				})
				m.allMessages = append([]Message{}, append(m.peerMessages, m.userMessages...)...)
				// Sort the combined slice by timestamp
				sort.Slice(m.allMessages, func(i, j int) bool {
					return m.allMessages[i].time.Before(m.allMessages[j].time)
				})
				m.mu.Unlock()

				return m, sendMessage(m.conn, m.remoteAddr, input)
			}

		case tea.KeyCtrlC:
			close(m.done)
			return m, tea.Quit

		// Handle regular typing
		default:
			var cmd tea.Cmd
			m.textInput, cmd = m.textInput.Update(msg)
			return m, cmd
		}

	// Handle incoming peer messages
	case Response:
		m.hoveredMessageIndex++

		if strings.HasPrefix(msg.text, "addr:") {
			var addr string
			_, _ = fmt.Sscanf(msg.text, "addr:%s", &addr)
			msg = Response{
				time: msg.time,
				ip:   bubblePinkAccentStyle.Render("(SYSTEM)") + " " + msg.ip,
				port: msg.port,
				text: addr,
			}
		}

		m.mu.Lock()
		m.peerMessages = append(m.peerMessages, Message(msg))
		m.allMessages = append([]Message{}, append(m.peerMessages, m.userMessages...)...)
		// Sort the combined slice by timestamp
		sort.Slice(m.allMessages, func(i, j int) bool {
			return m.allMessages[i].time.Before(m.allMessages[j].time)
		})
		m.mu.Unlock()

		return m, waitForMessages(m.sub)

	case Ping:
		m.lastPingTime = &msg.time
		return m, waitForPings(m.pingSub)

	// case ResizeMsg:
	// 	m.rows = msg.rows
	// 	m.cols = msg.cols - 3 // -3 because of the "> " prompt
	// 	m.textInput.Width = m.cols
	// 	return m, nil

	// Handle any other events
	default:
		return m, nil
	}
}

func (m *Model) View() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var output string

	// debug
	// output += "currentMessageIndex: " + strconv.Itoa(m.hoveredMessageIndex)
	// output += "\nhoveredMessage: " + m.hoveredMessage
	// output += "\ncopied: " + strconv.FormatBool(m.copied)
	// output += "\ntextInput.Value(): " + m.textInput.Value()
	// output += fmt.Sprintf("\nrows:%d cols:%d", m.rows, m.cols)
	// output += fmt.Sprintf("\nlast ping: %v", m.lastPingTime)
	// output += "\n\n"

	var copyButton string
	if m.copied {
		copyButton = buttonStyle.Render("Copied!")
	} else {
		copyButton = buttonStyle.Render("Copy")
	}

	// print every message like [timestamp] ip:port> text
	for i, message := range m.allMessages {
		// output += fmt.Sprintf("%s%s%s %s:%d%s %s",
		// 	bubblePinkAccentStyle.Render("["),
		// 	message.time.Format("15:04:05"),
		// 	bubblePinkAccentStyle.Render("]"),
		// 	message.ip,
		// 	message.port,
		// 	bubblePinkAccentStyle.Render(">"),
		// 	message.text,
		// )
		output += fmt.Sprintf("%s:%d %s%s%s",
			message.ip,
			message.port,
			bubblePinkAccentStyle.Render("["),
			message.time.Format("15:04"),
			bubblePinkAccentStyle.Render("]"),
		)
		if message.delivered {
			output += " ✓✓"
		}
		if i == m.hoveredMessageIndex {
			output += fmt.Sprintf(" %s\n", copyButton)
		} else {
			output += "\n"
		}
		output += fmt.Sprintf("%s %s\n\n", bubblePinkAccentStyle.Render("|"), message.text)
	}

	output += fmt.Sprintf("\n%s", m.textInput.View())

	return output
}

func main() {
	localPort := flag.Int("lport", 0, "Local port to bind to")
	remoteIP := flag.String("rip", "", "Remote IP address")
	remotePort := flag.Int("rport", 0, "Remote port")

	flag.Parse()

	// Validate flags
	if *localPort == 0 || *remoteIP == "" || *remotePort == 0 {
		fmt.Println("Error: All flags are required")
		fmt.Println("Usage:")
		flag.PrintDefaults()
		os.Exit(1)
	}

	// Validate environment variables
	discovery_ip := os.Getenv("discovery_ip")
	if discovery_ip == "" {
		fmt.Println("EnvVarError: discovery_ip not set")
		os.Exit(1)
	}

	localAddr := &net.UDPAddr{
		IP:   net.ParseIP("0.0.0.0"),
		Port: *localPort,
	}

	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		fmt.Printf("Failed to bind to port %d: %v\n", *localPort, err)
		os.Exit(1)
	}
	defer conn.Close()

	remoteAddr := &net.UDPAddr{
		IP:   net.ParseIP(*remoteIP),
		Port: *remotePort,
	}

	if remoteAddr.IP == nil {
		fmt.Printf("Invalid remote IP address: %s\n", *remoteIP)
		os.Exit(1)
	}

	done := make(chan struct{})

	// Start punching UDP holes in our router towards our peer
	go punchHoles(conn, remoteAddr, done)

	ti := textinput.New()
	ti.Placeholder = "Type something..."
	ti.Focus()
	ti.CharLimit = 256
	ti.Width = 50

	ti.Cursor.Style = bubblePinkAccentStyle
	ti.PromptStyle = bubblePinkAccentStyle

	p := tea.NewProgram(&Model{
		done:         done,
		localPort:    *localPort,
		conn:         conn,
		remoteAddr:   remoteAddr,
		sub:          make(chan Response),
		pingSub:      make(chan Ping),
		peerMessages: []Message{},
		userMessages: []Message{},
		textInput:    ti,
		discoveryAddr: &net.UDPAddr{
			IP:   net.ParseIP(discovery_ip),
			Port: 50000,
		},
	})

	// start polling the console's rows and columns
	// go pollConsoleSize(p)

	if _, err := p.Run(); err != nil {
		fmt.Printf("Uh oh, there was an error: %v\n", err)
		os.Exit(1)
	}
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
