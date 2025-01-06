package main

import (
	"flag"
	"fmt"
	"net"
	"os"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func sendPings(conn *net.UDPConn, remoteAddr *net.UDPAddr, done chan struct {}) {
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
    ip string
    port int
    text string
}

type Model struct {
    done chan struct{}
    sub chan Message // where we'll receive message notifications
    conn *net.UDPConn
    remoteAddr *net.UDPAddr
    localPort int
    peerMessages []Message
    userMessages []Message
    currentUserMessage Message
    textInput textinput.Model
}

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
                return nil
            default:
                conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
                n, addr, err := conn.ReadFromUDP(buffer)
                if err != nil {
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

type Response Message

// A command that waits for the messages on a channel.
func waitForMessages(sub chan Message) tea.Cmd {
	return func() tea.Msg {
		return Response(<-sub)
	}
}

func (m Model) Init() tea.Cmd {
    return tea.Batch(
        listenForMessages(m.sub, m.conn, m.done),
        waitForMessages(m.sub),
    )
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    var cmd tea.Cmd

    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.Type {
        case tea.KeyEnter:
            input := m.textInput.Value()
            if input == "" { return m, nil }
            if input == "/quit" {
                close(m.done)
                return m, tea.Quit
            }
            
            newMessage := Message{
                time: time.Now(),
                ip:   "localhost",
                port: m.localPort,
                text: m.currentUserMessage.text,
            }
            
            // Add to userMessages and clear current message
            m.userMessages = append(m.userMessages, newMessage)
            m.currentUserMessage.text = ""
            m.textInput.Reset()
            
            return m, sendMessage(m.conn, m.remoteAddr, newMessage.text)

        case tea.KeyCtrlC:
            close(m.done)
            return m, tea.Quit

        // Handle regular typing
        default:
            // if msg.Alt { return m, nil }
            m.currentUserMessage.text = m.textInput.Value() + msg.String()
            m.textInput, cmd = m.textInput.Update(msg)
            return m, cmd
        }

    // Handle incoming peer messages
    case Response:
        m.peerMessages = append(m.peerMessages, Message(msg))
        return m, waitForMessages(m.sub)

    // Handle any other events
    default:
        return m, nil
    }
}

func (m Model) View() string {
    allMessages := append(m.peerMessages, m.userMessages...)
    
    // Sort the combined slice by timestamp
	sort.Slice(allMessages, func(i, j int) bool {
		return allMessages[i].time.Before(allMessages[j].time)
	})
    
    // print every message like [timestamp] ip:port> text
    var output string
    for _, message := range allMessages {
        output += fmt.Sprintf("[%s] %s:%d> %s\n", message.time.Format("15:04:05"), message.ip, message.port, message.text)
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
    
    // fmt.Printf("Successfully bound to port %d\n", *localPort)
    
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
    go sendPings(conn, remoteAddr, done)

    ti := textinput.New()
    ti.Placeholder = "Type something..."
    ti.Focus()
    ti.CharLimit = 156
    ti.Width = 50
    
    // Customize the cursor
    ti.Cursor.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))
    ti.PromptStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("205"))

    if _, err := tea.NewProgram(Model{
        done: done,
        localPort: *localPort,
        conn: conn,
        remoteAddr: remoteAddr,
        sub: make(chan Message),
        peerMessages: []Message{},
        userMessages: []Message{},
        currentUserMessage: Message{
            text: "",
        },
        textInput: ti,
    }).Run(); err != nil {
        fmt.Printf("Uh oh, there was an error: %v\n", err)
        os.Exit(1)
    }
}