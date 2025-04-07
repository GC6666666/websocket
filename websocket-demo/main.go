package main

import (
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// 配置升级器
var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true }, // 允许跨域
}

// 消息结构
type Message struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Sender  string `json:"sender"`
	Time    int64  `json:"time"`
}

// 客户端连接
type Client struct {
	ID       string
	Conn     *websocket.Conn
	SendChan chan Message
}

// 聊天室
type ChatRoom struct {
	clients    map[string]*Client
	register   chan *Client
	unregister chan *Client
	broadcast  chan Message
	mutex      sync.Mutex
}

// 创建新的聊天室
func NewChatRoom() *ChatRoom {
	return &ChatRoom{
		clients:    make(map[string]*Client),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		broadcast:  make(chan Message),
	}
}

// 运行聊天室
func (cr *ChatRoom) Run() {
	for {
		select {
		case client := <-cr.register:
			cr.mutex.Lock()
			cr.clients[client.ID] = client
			cr.mutex.Unlock()
			log.Printf("新客户端 %s 已连接. 当前连接数: %d", client.ID, len(cr.clients))

			// 发送欢迎消息
			welcomeMsg := Message{
				Type:    "system",
				Content: "欢迎来到聊天室！",
				Sender:  "System",
				Time:    time.Now().Unix(),
			}
			client.SendChan <- welcomeMsg

			// 广播新用户加入消息
			joinMsg := Message{
				Type:    "system",
				Content: "用户 " + client.ID + " 加入了聊天室",
				Sender:  "System",
				Time:    time.Now().Unix(),
			}
			cr.broadcast <- joinMsg

		case client := <-cr.unregister:
			cr.mutex.Lock()
			if _, ok := cr.clients[client.ID]; ok {
				delete(cr.clients, client.ID)
				close(client.SendChan)
			}
			cr.mutex.Unlock()
			log.Printf("客户端 %s 已断开. 剩余连接数: %d", client.ID, len(cr.clients))

			// 广播用户离开消息
			leaveMsg := Message{
				Type:    "system",
				Content: "用户 " + client.ID + " 离开了聊天室",
				Sender:  "System",
				Time:    time.Now().Unix(),
			}
			cr.broadcast <- leaveMsg

		case msg := <-cr.broadcast:
			cr.mutex.Lock()
			for _, client := range cr.clients {
				select {
				case client.SendChan <- msg:
				default:
					close(client.SendChan)
					delete(cr.clients, client.ID)
				}
			}
			cr.mutex.Unlock()
		}
	}
}

// 客户端读取处理
func (c *Client) readPump(cr *ChatRoom) {
	defer func() {
		cr.unregister <- c
		c.Conn.Close()
	}()

	c.Conn.SetReadLimit(512) // 限制最大消息大小
	c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.Conn.SetPongHandler(func(string) error {
		c.Conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		var msg Message
		err := c.Conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) {
				log.Printf("读取错误: %v", err)
			}
			break
		}

		// 设置消息发送者和时间
		msg.Sender = c.ID
		msg.Time = time.Now().Unix()

		log.Printf("收到消息: %+v", msg)
		cr.broadcast <- msg
	}
}

// 客户端写入处理
func (c *Client) writePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.Conn.Close()
	}()

	for {
		select {
		case msg, ok := <-c.SendChan:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				// 通道已关闭
				c.Conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			err := c.Conn.WriteJSON(msg)
			if err != nil {
				log.Printf("写入错误: %v", err)
				return
			}

		case <-ticker.C:
			c.Conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// WebSocket处理器
func handleWebSocket(cr *ChatRoom, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("升级连接失败: %v", err)
		return
	}

	// 创建客户端ID
	clientID := r.URL.Query().Get("username")
	if clientID == "" {
		clientID = "匿名用户_" + time.Now().Format("150405")
	}

	client := &Client{
		ID:       clientID,
		Conn:     conn,
		SendChan: make(chan Message, 256),
	}

	cr.register <- client

	// 启动客户端处理
	go client.writePump()
	go client.readPump(cr)
}

// HTML页面内容
const htmlContent = `<!DOCTYPE html>
<html>
<head>
    <title>WebSocket聊天室</title>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif; }
        .chat-container { max-width: 700px; margin: 20px auto; border: 1px solid #ddd; border-radius: 8px; overflow: hidden; }
        .chat-header { background-color: #4a69bd; color: white; padding: 15px; text-align: center; font-size: 18px; }
        .chat-messages { padding: 15px; height: 400px; overflow-y: auto; background-color: #f9f9f9; }
        .message { margin-bottom: 10px; padding: 8px 12px; border-radius: 18px; max-width: 80%; word-wrap: break-word; }
        .message.system { background-color: #f1f2f6; color: #747d8c; text-align: center; max-width: 100%; font-style: italic; }
        .message.incoming { background-color: #dfe4ea; color: #2f3542; align-self: flex-start; float: left; }
        .message.outgoing { background-color: #4a69bd; color: white; align-self: flex-end; float: right; }
        .sender { font-size: 12px; color: #57606f; margin-bottom: 4px; }
        .message.outgoing .sender { color: #dcdde1; }
        .message-content { font-size: 14px; }
        .time { font-size: 10px; margin-top: 4px; text-align: right; }
        .message.outgoing .time { color: #dcdde1; }
        .input-area { padding: 15px; background-color: white; display: flex; }
        .input-area input { flex-grow: 1; border: 1px solid #ddd; border-radius: 20px; padding: 10px 15px; outline: none; }
        .input-area button { background-color: #4a69bd; color: white; border: none; border-radius: 20px; padding: 10px 15px; margin-left: 10px; cursor: pointer; }
        .input-area button:hover { background-color: #3c5aa8; }
        .clearfix:after { content: ""; display: table; clear: both; }
        .chat-login { padding: 20px; background-color: #f9f9f9; text-align: center; }
        .chat-login input { padding: 10px; width: 70%; margin-right: 10px; border: 1px solid #ddd; border-radius: 4px; }
        .chat-login button { padding: 10px 15px; background-color: #4a69bd; color: white; border: none; border-radius: 4px; cursor: pointer; }
    </style>
</head>
<body>
    <div class="chat-container">
        <div class="chat-header">
            WebSocket聊天室
        </div>
        
        <div id="login-screen" class="chat-login">
            <h3>请输入你的用户名</h3>
            <div style="margin-top: 20px;">
                <input type="text" id="username" placeholder="请输入用户名...">
                <button id="login-btn">进入聊天室</button>
            </div>
        </div>
        
        <div id="chat-screen" style="display: none;">
            <div id="messages" class="chat-messages"></div>
            
            <div class="input-area">
                <input id="message-input" type="text" placeholder="输入消息..." autocomplete="off">
                <button id="send-btn">发送</button>
            </div>
        </div>
    </div>

    <script>
        document.addEventListener('DOMContentLoaded', function() {
            const loginScreen = document.getElementById('login-screen');
            const chatScreen = document.getElementById('chat-screen');
            const usernameInput = document.getElementById('username');
            const loginBtn = document.getElementById('login-btn');
            const messagesContainer = document.getElementById('messages');
            const messageInput = document.getElementById('message-input');
            const sendBtn = document.getElementById('send-btn');
            
            let ws;
            let username = '';
            
            // 登录处理
            loginBtn.addEventListener('click', function() {
                username = usernameInput.value.trim();
                if (username) {
                    loginScreen.style.display = 'none';
                    chatScreen.style.display = 'block';
                    connectWebSocket(username);
                }
            });
            
            usernameInput.addEventListener('keypress', function(e) {
                if (e.key === 'Enter') {
                    loginBtn.click();
                }
            });
            
            function connectWebSocket(username) {
                // 建立WebSocket连接
                const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
                ws = new WebSocket(protocol + '//' + window.location.host + '/ws?username=' + encodeURIComponent(username));
                
                ws.onopen = function() {
                    console.log('WebSocket连接已建立');
                };
                
                ws.onmessage = function(event) {
                    const message = JSON.parse(event.data);
                    displayMessage(message);
                };
                
                ws.onclose = function() {
                    console.log('WebSocket连接已关闭');
                    addSystemMessage('连接已断开，请刷新页面重连');
                };
                
                ws.onerror = function(error) {
                    console.error('WebSocket错误:', error);
                    addSystemMessage('连接发生错误');
                };
            }
            
            // 发送消息
            sendBtn.addEventListener('click', sendMessage);
            messageInput.addEventListener('keypress', function(e) {
                if (e.key === 'Enter') {
                    sendMessage();
                }
            });
            
            function sendMessage() {
                const content = messageInput.value.trim();
                if (content && ws && ws.readyState === WebSocket.OPEN) {
                    const message = {
                        type: 'chat',
                        content: content,
                        time: Math.floor(Date.now() / 1000)
                    };
                    
                    ws.send(JSON.stringify(message));
                    messageInput.value = '';
                }
            }
            
            // 显示消息
            function displayMessage(message) {
                const messageDiv = document.createElement('div');
                const isCurrentUser = message.sender === username;
                
                if (message.type === 'system') {
                    messageDiv.className = 'message system';
                    messageDiv.innerHTML = '<div class="message-content">' + message.content + '</div>';
                } else {
                    messageDiv.className = isCurrentUser ? 'message outgoing' : 'message incoming';
                    
                    const time = new Date(message.time * 1000).toLocaleTimeString([], {hour: '2-digit', minute:'2-digit'});
                    messageDiv.innerHTML = 
                        '<div class="sender">' + message.sender + '</div>' +
                        '<div class="message-content">' + message.content + '</div>' +
                        '<div class="time">' + time + '</div>';
                }
                
                messagesContainer.appendChild(messageDiv);
                messagesContainer.appendChild(document.createElement('div')).className = 'clearfix';
                
                // 滚动到底部
                messagesContainer.scrollTop = messagesContainer.scrollHeight;
            }
            
            function addSystemMessage(content) {
                displayMessage({
                    type: 'system',
                    content: content,
                    sender: 'System',
                    time: Math.floor(Date.now() / 1000)
                });
            }
        });
    </script>
</body>
</html>`

// 服务静态文件
func handleHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(htmlContent))
}

func main() {
	// 创建并启动聊天室
	chatRoom := NewChatRoom()
	go chatRoom.Run()

	// 设置路由
	http.HandleFunc("/", handleHome)
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		handleWebSocket(chatRoom, w, r)
	})

	// 启动服务器
	addr := ":8081"
	log.Printf("启动聊天服务器，地址: %s", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}
