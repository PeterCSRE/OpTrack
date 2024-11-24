package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// JiraTicket represents a JIRA ticket and its associated operators
type JiraTicket struct {
	ID        string    `json:"id"`
	Operators []string  `json:"operators"`
	Added     time.Time `json:"added"`
}

// QuayTagInfo represents a single tag in the Quay.io API response
type QuayTagInfo struct {
	Name           string `json:"name"`
	LastModified   string `json:"last_modified"`
	ManifestDigest string `json:"manifest_digest"`
}

// QuayTagResponse represents the Quay.io API response
type QuayTagResponse struct {
	Tags []QuayTagInfo `json:"tags"`
}

// OperatorStatus represents the status of an operator in Quay.io
type OperatorStatus struct {
	Name        string    `json:"name"`
	LastUpdated time.Time `json:"lastUpdated"`
	SHA256      string    `json:"sha256"`
	Status      string    `json:"status"`
}

// AppState maintains the application's state in memory
type AppState struct {
	Tickets map[string]JiraTicket
	mu      sync.RWMutex
}

// QuayClient handles communication with Quay.io API
type QuayClient struct {
	HTTPClient *http.Client
}

func NewQuayClient() *QuayClient {
	return &QuayClient{
		HTTPClient: &http.Client{Timeout: 10 * time.Second},
	}
}

func (qc *QuayClient) GetOperatorStatus(operator string) (*OperatorStatus, error) {
	parts := strings.Split(operator, "/")
	if len(parts) != 2 {
		return &OperatorStatus{
			Name:   operator,
			Status: "Invalid format. Expected: namespace/repository",
		}, nil
	}

	namespace, repository := parts[0], parts[1]
	url := fmt.Sprintf("https://quay.io/api/v1/repository/%s/%s/tag/", namespace, repository)

	resp, err := qc.HTTPClient.Get(url)
	if err != nil {
		return &OperatorStatus{
			Name:   operator,
			Status: "Failed to connect to Quay.io",
		}, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return &OperatorStatus{
			Name:   operator,
			Status: fmt.Sprintf("Quay.io error: %d", resp.StatusCode),
		}, nil
	}

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return &OperatorStatus{
			Name:   operator,
			Status: "Failed to read response",
		}, nil
	}

	// Debug logging
	log.Printf("Raw Quay.io response for %s: %s", operator, string(body))

	var tagResponse QuayTagResponse
	if err := json.Unmarshal(body, &tagResponse); err != nil {
		log.Printf("Failed to parse JSON: %v", err)
		return &OperatorStatus{
			Name:   operator,
			Status: fmt.Sprintf("Parse error: %v", err),
		}, nil
	}

	if len(tagResponse.Tags) == 0 {
		return &OperatorStatus{
			Name:   operator,
			Status: "No tags found",
		}, nil
	}

	// Find the most recent tag
	var latestTag QuayTagInfo
	latestTime := time.Time{}

	for _, tag := range tagResponse.Tags {
		tagTime, err := time.Parse(time.RFC1123Z, tag.LastModified)
		if err != nil {
			log.Printf("Failed to parse time %s: %v", tag.LastModified, err)
			continue
		}
		if tagTime.After(latestTime) {
			latestTime = tagTime
			latestTag = tag
		}
	}

	if latestTime.IsZero() {
		return &OperatorStatus{
			Name:   operator,
			Status: "No valid timestamps found",
		}, nil
	}

	return &OperatorStatus{
		Name:        operator,
		LastUpdated: latestTime,
		SHA256:      strings.TrimPrefix(latestTag.ManifestDigest, "sha256:"),
		Status:      "OK",
	}, nil
}

func main() {
	state := &AppState{
		Tickets: make(map[string]JiraTicket),
	}

	quayClient := NewQuayClient()

	fs := http.FileServer(http.Dir("static"))
	http.Handle("/static/", http.StripPrefix("/static/", fs))

	http.HandleFunc("/api/tickets", state.handleTickets)
	http.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		state.handleStatus(w, r, quayClient)
	})
	http.HandleFunc("/", serveTemplate)

	log.Println("Server starting on :8080")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func serveTemplate(w http.ResponseWriter, r *http.Request) {
	tmpl := `
<!DOCTYPE html>
<html>
<head>
    <title>Operator Update Tracker</title>
    <style>
        .container { display: flex; }
        .nav { width: 250px; padding: 20px; border-right: 1px solid #ccc; }
        .content { flex: 1; padding: 20px; }
        .ticket-item { 
            display: flex; 
            justify-content: space-between;
            align-items: center;
            padding: 10px;
            margin-bottom: 5px;
            border: 1px solid #eee;
        }
        .ticket-item:hover { background-color: #f0f0f0; }
        .ticket-name { cursor: pointer; flex-grow: 1; }
        .delete-btn {
            color: red;
            cursor: pointer;
            padding: 0 5px;
        }
        .add-button { font-size: 24px; cursor: pointer; margin-bottom: 20px; }
        .form-group { margin-bottom: 15px; }
        .hidden { display: none; }
        .error { color: red; }
        .ok { color: green; }
    </style>
</head>
<body>
    <div class="container">
        <div class="nav">
            <div class="add-button" onclick="showAddForm()">+ New Ticket</div>
            <div id="ticketList"></div>
        </div>
        <div class="content">
            <div id="addForm" class="hidden">
                <h2>Add New Ticket</h2>
                <div class="form-group">
                    <label>JIRA Ticket #:</label>
                    <input type="text" id="jiraId">
                </div>
                <div class="form-group">
                    <label>Operators (comma-separated):</label>
                    <input type="text" id="operators" placeholder="namespace/repository, e.g., app-sre/splunk-audit-exporter">
                </div>
                <button onclick="addTicket()">Add</button>
            </div>
            <div id="statusDisplay"></div>
        </div>
    </div>
    
    <script>
    function showAddForm() {
        document.getElementById('addForm').classList.remove('hidden');
        document.getElementById('statusDisplay').classList.add('hidden');
    }
    
    function addTicket() {
        const jiraId = document.getElementById('jiraId').value;
        const operators = document.getElementById('operators').value;
        
        fetch('/api/tickets', {
            method: 'POST',
            headers: {'Content-Type': 'application/json'},
            body: JSON.stringify({
                id: jiraId,
                operators: operators.split(',').map(op => op.trim())
            })
        })
        .then(response => response.json())
        .then(data => {
            loadTickets();
            document.getElementById('jiraId').value = '';
            document.getElementById('operators').value = '';
        });
    }
    
    function deleteTicket(event, ticketId) {
        event.stopPropagation();
        if (confirm('Are you sure you want to delete this ticket?')) {
            fetch('/api/tickets?id=' + encodeURIComponent(ticketId), {
                method: 'DELETE'
            })
            .then(response => {
                if (response.ok) {
                    loadTickets();
                    document.getElementById('statusDisplay').innerHTML = '';
                }
            });
        }
    }
    
    function loadTickets() {
        fetch('/api/tickets')
        .then(response => response.json())
        .then(tickets => {
            const list = document.getElementById('ticketList');
            list.innerHTML = '';
            Object.entries(tickets).forEach(([id, ticket]) => {
                const div = document.createElement('div');
                div.className = 'ticket-item';
                
                const nameSpan = document.createElement('span');
                nameSpan.className = 'ticket-name';
                nameSpan.textContent = id;
                nameSpan.onclick = () => loadStatus(id);
                
                const deleteBtn = document.createElement('span');
                deleteBtn.className = 'delete-btn';
                deleteBtn.textContent = '×';
                deleteBtn.onclick = (e) => deleteTicket(e, id);
                
                div.appendChild(nameSpan);
                div.appendChild(deleteBtn);
                list.appendChild(div);
            });
        });
    }
    
    function loadStatus(ticketId) {
        document.getElementById('addForm').classList.add('hidden');
        const statusDisplay = document.getElementById('statusDisplay');
        statusDisplay.classList.remove('hidden');
        statusDisplay.innerHTML = '<div>Loading...</div>';
        
        fetch('/api/status?ticket=' + encodeURIComponent(ticketId))
        .then(response => response.json())
        .then(statuses => {
            let html = '<h2>Status for ' + ticketId + '</h2>';
            html += '<table border="1" style="width: 100%; border-collapse: collapse;">';
            html += '<tr><th>Operator</th><th>Last Updated</th><th>SHA256</th><th>Status</th></tr>';
            
            statuses.forEach(status => {
                const statusClass = status.status === 'OK' ? 'ok' : 'error';
                html += '<tr>';
                html += '<td>' + status.name + '</td>';
                html += '<td>' + (status.lastUpdated ? new Date(status.lastUpdated).toLocaleString() : 'N/A') + '</td>';
                html += '<td style="font-family: monospace; word-break: break-all;">' + (status.sha256 || 'N/A') + '</td>';
                html += '<td class="' + statusClass + '">' + status.status + '</td>';
                html += '</tr>';
            });
            
            html += '</table>';
            statusDisplay.innerHTML = html;
        });
    }
    
    // Load tickets on page load
    loadTickets();
    </script>
</body>
</html>`

	t := template.Must(template.New("index").Parse(tmpl))
	t.Execute(w, nil)
}

func (s *AppState) handleTickets(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	switch r.Method {
	case "GET":
		json.NewEncoder(w).Encode(s.Tickets)

	case "POST":
		var ticket JiraTicket
		if err := json.NewDecoder(r.Body).Decode(&ticket); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		ticket.Added = time.Now()
		s.Tickets[ticket.ID] = ticket

		json.NewEncoder(w).Encode(ticket)

	case "DELETE":
		ticketID := r.URL.Query().Get("id")
		if ticketID == "" {
			http.Error(w, "Ticket ID required", http.StatusBadRequest)
			return
		}

		delete(s.Tickets, ticketID)
		w.WriteHeader(http.StatusOK)
	}
}

func (s *AppState) handleStatus(w http.ResponseWriter, r *http.Request, qc *QuayClient) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ticketID := r.URL.Query().Get("ticket")

	s.mu.RLock()
	ticket, exists := s.Tickets[ticketID]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, "Ticket not found", http.StatusNotFound)
		return
	}

	statuses := make([]OperatorStatus, 0, len(ticket.Operators))
	for _, operator := range ticket.Operators {
		status, err := qc.GetOperatorStatus(operator)
		if err != nil {
			log.Printf("Error getting status for operator %s: %v", operator, err)
			status = &OperatorStatus{
				Name:   operator,
				Status: fmt.Sprintf("Error: %v", err),
			}
		}
		statuses = append(statuses, *status)
	}

	json.NewEncoder(w).Encode(statuses)
}
