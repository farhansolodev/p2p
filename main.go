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
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	// "golang.org/x/sys/windows"
)

func punchHoles(conn *net.UDPConn, remoteAddr *net.UDPAddr, done chan struct{}) {
	ticker := time.NewTicker(1 * time.Second)
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

type Message struct {
	time time.Time
	ip   string
	port int
	text string
}

type Response Message

// type ResizeMsg struct {
// 	rows int
// 	cols int
// }

type Model struct {
	mu                  sync.Mutex    // Protects concurrent access to messages
	done                chan struct{} // Signals shutdown to background goroutines
	sub                 chan Message  // Channel for receiving message notifications
	conn                *net.UDPConn
	remoteAddr          *net.UDPAddr
	localPort           int
	peerMessages        []Message
	userMessages        []Message
	allMessages         []Message
	textInput           textinput.Model
	discoveryAddr       *net.UDPAddr
	hoveredMessageIndex int
	hoveredMessage      string
	copied              bool
	// rows                int
	// cols                int
}

var keys = struct {
	Escape key.Binding
}{
	Escape: key.NewBinding(
		key.WithKeys("esc"),
	),
}

var (
	bubblePinkAccentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
	buttonStyle           = lipgloss.NewStyle().Foreground(lipgloss.Color("#000000")).Background(lipgloss.Color("#00ff00"))
)

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

func sendMessage(conn *net.UDPConn, remoteAddr *net.UDPAddr, message string) tea.Cmd {
	return func() tea.Msg {
		_, _ = conn.WriteToUDP([]byte(message), remoteAddr)
		return nil
	}
}

func listenForMessages(sub chan Message, conn *net.UDPConn, done chan struct{}) tea.Cmd {
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

				if string(buffer[:n]) != "ping" {
					sub <- Message{
						time: time.Now(),
						ip:   addr.IP.String(),
						port: addr.Port,
						text: string(buffer[:n]),
					}
				}
			}
		}
	}
}

// A command that waits for the messages on a channel.
func waitForMessages(sub chan Message) tea.Cmd {
	return func() tea.Msg {
		return Response(<-sub)
	}
}

func requestAddress(conn *net.UDPConn, discoveryAddr *net.UDPAddr) tea.Cmd {
	_, _ = conn.WriteToUDP([]byte("whoami"), discoveryAddr)
	return nil
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(
		listenForMessages(m.sub, m.conn, m.done),
		waitForMessages(m.sub),
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
			if m.hoveredMessageIndex < len(m.allMessages) && len(m.allMessages) > 0 {
				_ = clipboard.WriteAll(m.hoveredMessage)
				m.copied = true
				return m, nil
			}

			input := m.textInput.Value()
			if input == "" {
				return m, nil
			}
			if input == "/quit" {
				close(m.done)
				return m, tea.Quit
			}

			switch input {
			case "/getaddr":
				m.textInput.Reset()
				return m, requestAddress(m.conn, m.discoveryAddr)
			default:
				m.hoveredMessageIndex++

				m.mu.Lock()
				m.userMessages = append(m.userMessages, Message{
					time: time.Now(),
					ip:   bubblePinkAccentStyle.Render("(You)") + " localhost",
					port: m.localPort,
					text: input,
				})
				m.allMessages = append([]Message{}, append(m.peerMessages, m.userMessages...)...)
				// Sort the combined slice by timestamp
				sort.Slice(m.allMessages, func(i, j int) bool {
					return m.allMessages[i].time.Before(m.allMessages[j].time)
				})
				m.mu.Unlock()
				m.copied = false

				m.textInput.Reset()
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
	// output += fmt.Sprintf("rows:%d cols:%d", m.rows, m.cols)
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
		if i == m.hoveredMessageIndex {
			output += fmt.Sprintf(" %s\n", copyButton)
		} else {
			output += "\n"
		}
		output += message.text + "\n\n"
	}

	output += fmt.Sprintf("\n%s", m.textInput.View())

	return output
}

func main() {
	// Define flags
	localPort := flag.Int("lport", 0, "Local port to bind to")
	remoteIP := flag.String("rip", "", "Remote IP address")
	remotePort := flag.Int("rport", 0, "Remote port")

	// Parse flags
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

	// Create a UDP address for the local endpoint
	localAddr := &net.UDPAddr{
		IP:   net.ParseIP("0.0.0.0"),
		Port: *localPort,
	}

	// Create a UDP connection bound to the specific interface and port
	conn, err := net.ListenUDP("udp", localAddr)
	if err != nil {
		fmt.Printf("Failed to bind to port %d: %v\n", *localPort, err)
		os.Exit(1)
	}
	defer conn.Close()

	// Define the remote endpoint
	remoteAddr := &net.UDPAddr{
		IP:   net.ParseIP(*remoteIP),
		Port: *remotePort,
	}

	// Validate remote IP
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

	// Customize the cursor
	ti.Cursor.Style = bubblePinkAccentStyle
	ti.PromptStyle = bubblePinkAccentStyle

	p := tea.NewProgram(&Model{
		done:         done,
		localPort:    *localPort,
		conn:         conn,
		remoteAddr:   remoteAddr,
		sub:          make(chan Message),
		peerMessages: []Message{},
		userMessages: []Message{},
		textInput:    ti,
		discoveryAddr: &net.UDPAddr{
			IP:   net.ParseIP(discovery_ip),
			Port: 50000,
		},
	})

	// start polling the console's rows and columns
	// go func() {
	// 	pollConsoleSize(p)
	// }()

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
