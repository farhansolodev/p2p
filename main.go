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

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
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

type Model struct {
	mu            sync.Mutex    // Protects concurrent access to messages
	done          chan struct{} // Signals shutdown to background goroutines
	sub           chan Message  // Channel for receiving message notifications
	conn          *net.UDPConn
	remoteAddr    *net.UDPAddr
	localPort     int
	peerMessages  []Message
	userMessages  []Message
	textInput     textinput.Model
	discoveryAddr net.UDPAddr
}

var keys = struct {
	Escape key.Binding
}{
	Escape: key.NewBinding(
		key.WithKeys("esc"),
		key.WithHelp("esc", "close"),
	),
}

var pinkStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

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

func requestAddress(conn *net.UDPConn, discoveryAddr net.UDPAddr) tea.Cmd {
	_, _ = conn.WriteToUDP([]byte("whoami"), &discoveryAddr)
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
		case tea.KeyEnter:
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
				m.mu.Lock()
				m.userMessages = append(m.userMessages, Message{
					time: time.Now(),
					ip:   pinkStyle.Render("(You)") + " localhost",
					port: m.localPort,
					text: input,
				})
				m.mu.Unlock()

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
		if strings.HasPrefix(msg.text, "addr:") {
			var addr string
			_, _ = fmt.Sscanf(msg.text, "addr:%s", &addr)
			msg = Response{
				time: msg.time,
				ip:   pinkStyle.Render("(SYSTEM)") + " " + msg.ip,
				port: msg.port,
				text: fmt.Sprintf("Your external address is %s", addr),
			}
			// m.mu.Lock()
			// m.peerMessages = append(m.peerMessages, Message(msg))
			// m.mu.Unlock()
			// return m, tea.Batch(
			// 	sendMessage(m.conn, m.remoteAddr, fmt.Sprintf("%s My external address is %s", pinkStyle.Render("SYSTEM:"), addr)),
			// 	waitForMessages(m.sub),
			// )
			// } else {
		}
		m.mu.Lock()
		m.peerMessages = append(m.peerMessages, Message(msg))
		m.mu.Unlock()
		return m, waitForMessages(m.sub)

	// Handle any other events
	default:
		return m, nil
	}
}

func (m *Model) View() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	var output string

	allMessages := append([]Message{}, append(m.peerMessages, m.userMessages...)...)

	// Sort the combined slice by timestamp
	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].time.Before(allMessages[j].time)
	})

	// print every message like [timestamp] ip:port> text
	for _, message := range allMessages {
		output += fmt.Sprintf("%s%s%s %s:%d%s %s\n",
			pinkStyle.Render("["),
			message.time.Format("15:04:05"),
			pinkStyle.Render("]"),
			message.ip,
			message.port,
			pinkStyle.Render(">"),
			message.text,
		)
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

	// Start the ping goroutine
	go punchHoles(conn, remoteAddr, done)

	ti := textinput.New()
	ti.Placeholder = "Type something..."
	ti.Focus()
	ti.CharLimit = 156
	ti.Width = 50

	// Customize the cursor
	ti.Cursor.Style = pinkStyle
	ti.PromptStyle = pinkStyle

	if _, err := tea.NewProgram(&Model{
		done:         done,
		localPort:    *localPort,
		conn:         conn,
		remoteAddr:   remoteAddr,
		sub:          make(chan Message),
		peerMessages: []Message{},
		userMessages: []Message{},
		textInput:    ti,
		discoveryAddr: *&net.UDPAddr{
			IP:   net.ParseIP(discovery_ip),
			Port: 50000,
		},
	}).Run(); err != nil {
		fmt.Printf("Uh oh, there was an error: %v\n", err)
		os.Exit(1)
	}
}
