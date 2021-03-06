package bitfinex

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

// Pairs available
const (
	// Pairs
	BTCUSD = "BTCUSD"
	LTCUSD = "LTCUSD"
	LTCBTC = "LTCBTC"
	ETHUSD = "ETHUSD"
	ETHBTC = "ETHBTC"
	ETCUSD = "ETCUSD"
	ETCBTC = "ETCBTC"
	BFXUSD = "BFXUSD"
	BFXBTC = "BFXBTC"
	ZECUSD = "ZECUSD"
	ZECBTC = "ZECBTC"
	XMRUSD = "XMRUSD"
	XMRBTC = "XMRBTC"
	RRTUSD = "RRTUSD"
	RRTBTC = "RRTBTC"

	// Channels
	CHAN_BOOK   = "book"
	CHAN_TRADE  = "trades"
	CHAN_TICKER = "ticker"
)

// WebSocketService allow to connect and receive stream data
// from bitfinex.com ws service.
type WebSocketService struct {
	// http client
	client *Client
	// websocket client
	ws *websocket.Conn
	// special web socket for private messages
	privateWs *websocket.Conn
	// map internal channels to websocket's
	chanMap    map[float64]chan [][]float64
	subscribes []subscribeToChannel
}

type SubscribeMsg struct {
	Event   string  `json:"event"`
	Channel string  `json:"channel"`
	Pair    string  `json:"pair"`
	Len     string  `json:"len"`
	ChanId  float64 `json:"chanId,omitempty"`
}

type subscribeToChannel struct {
	Channel string
	Pair    string
	Len     int
	Chan    chan [][]float64
}

func NewWebSocketService(c *Client) *WebSocketService {
	return &WebSocketService{
		client:     c,
		chanMap:    make(map[float64]chan [][]float64),
		subscribes: make([]subscribeToChannel, 0),
	}
}

// Connect create new bitfinex websocket connection
func (w *WebSocketService) Connect() error {
	var d = websocket.Dialer{
		Subprotocols:     []string{"p1", "p2"},
		ReadBufferSize:   1024,
		WriteBufferSize:  1024,
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 3 * time.Second,
	}

	if w.client.WebSocketTLSSkipVerify {
		d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	ws, _, err := d.Dial(w.client.WebSocketURL, nil)
	if err != nil {
		return err
	}
	w.ws = ws
	return nil
}

// Close web socket connection
func (w *WebSocketService) Close() {
	w.ws.Close()
}

func (w *WebSocketService) AddSubscribe(channel string, pair string, length int, c chan [][]float64) {
	s := subscribeToChannel{
		Channel: channel,
		Pair:    pair,
		Chan:    c,
		Len:     length,
	}
	w.subscribes = append(w.subscribes, s)
}

func (w *WebSocketService) ClearSubscriptions() {
	w.subscribes = make([]subscribeToChannel, 0)
}

func (w *WebSocketService) sendSubscribeMessages() error {
	for _, s := range w.subscribes {
		msg, _ := json.Marshal(SubscribeMsg{
			Event:   "subscribe",
			Channel: s.Channel,
			Pair:    s.Pair,
			Len:     strconv.Itoa(s.Len),
		})
		err := w.ws.WriteMessage(websocket.TextMessage, msg)
		if err != nil {
			// Can't send message to web socket.
			return err
		}
	}
	return nil
}

// Watch allows to subsribe to channels and watch for new updates.
// This method supports next channels: book, trade, ticker.
func (w *WebSocketService) Subscribe() error {
	// Subscribe to each channel
	if err := w.sendSubscribeMessages(); err != nil {
		return err
	}

	var msg string

	for {
		_, p, err := w.ws.ReadMessage()
		msg = string(p)
		if err != nil {
			return err
		}
		if strings.Contains(msg, "event") {
			w.handleEventMessage(msg)
		} else {
			w.handleDataMessage(msg)
		}
	}

	return nil
}

func (w *WebSocketService) handleEventMessage(msg string) {
	// Check for first message(event:subscribed)
	event := &SubscribeMsg{}
	err := json.Unmarshal([]byte(msg), &event)

	// Received "subscribed" resposne. Link channels.
	if err == nil {
		for _, k := range w.subscribes {
			if event.Event == "subscribed" && event.Pair == k.Pair && event.Channel == k.Channel {
				w.chanMap[event.ChanId] = k.Chan
			}
		}
	}
}

func (w *WebSocketService) handleDataMessage(msg string) {

	// Received payload or data update
	var dataUpdate []float64
	err := json.Unmarshal([]byte(msg), &dataUpdate)
	if err == nil {
		chanId := dataUpdate[0]
		// Remove chanId from data update
		// and send message to internal chan
		w.chanMap[chanId] <- [][]float64{dataUpdate[1:]}
	}

	// Payload received
	var fullPayload []interface{}
	err = json.Unmarshal([]byte(msg), &fullPayload)

	if err != nil {
		log.Println("Error decoding fullPayload", err)
	} else {
		if len(fullPayload) > 3 {
			itemsSlice := fullPayload[3:]
			i, _ := json.Marshal(itemsSlice)
			var item []float64
			err = json.Unmarshal(i, &item)
			if err == nil {
				chanID := fullPayload[0].(float64)
				w.chanMap[chanID] <- [][]float64{item}
			}
		} else {
			itemsSlice := fullPayload[1]
			i, _ := json.Marshal(itemsSlice)
			var items [][]float64
			err = json.Unmarshal(i, &items)
			if err == nil {
				chanId := fullPayload[0].(float64)
				// we need to say the receiver, that we've got the entire book.
				// normally, in this case it should reset the old book.
				w.chanMap[chanId] <- append([][]float64{[]float64{0, 0, 0}}, items...)
			}
		}
	}
}

/////////////////////////////
// Private websocket messages
/////////////////////////////

type privateConnect struct {
	Event       string `json:"event"`
	ApiKey      string `json:"apiKey"`
	AuthSig     string `json:"authSig"`
	AuthPayload string `json:"authPayload"`
}

// Private channel auth response
type privateResponse struct {
	Event  string  `json:"event"`
	Status string  `json:"status"`
	ChanId float64 `json:"chanId,omitempty"`
	UserId float64 `json:"userId"`
}

type TermData struct {
	// Data term. E.g: ps, ws, ou, etc... See official documentation for more details.
	Term string
	// Data will contain different number of elements for each term.
	// Examples:
	// Term: ws, Data: ["exchange","BTC",0.01410829,0]
	// Term: oc, Data: [0,"BTCUSD",0,-0.01,"","CANCELED",270,0,"2015-10-15T11:26:13Z",0]
	Data  []interface{}
	Error string
}

func (c *TermData) HasError() bool {
	return len(c.Error) > 0
}

func (w *WebSocketService) ConnectPrivate(ch chan TermData) {

	var d = websocket.Dialer{
		Subprotocols:    []string{"p1", "p2"},
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		Proxy:           http.ProxyFromEnvironment,
	}

	ws, _, err := d.Dial(w.client.WebSocketURL, nil)

	if w.client.WebSocketTLSSkipVerify {
		d.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}

	ws, _, err = d.Dial(w.client.WebSocketURL, nil)
	if err != nil {
		ch <- TermData{
			Error: err.Error(),
		}
		return
	}

	payload := "AUTH" + fmt.Sprintf("%v", time.Now().Unix())
	connectMsg, _ := json.Marshal(&privateConnect{
		Event:       "auth",
		ApiKey:      w.client.ApiKey,
		AuthSig:     w.client.signPayload(payload),
		AuthPayload: payload,
	})

	// Send auth message
	err = ws.WriteMessage(websocket.TextMessage, connectMsg)
	if err != nil {
		ch <- TermData{
			Error: err.Error(),
		}
		ws.Close()
		return
	}

	var msg string
	for {
		_, p, err := ws.ReadMessage()
		if err != nil {
			ch <- TermData{
				Error: err.Error(),
			}
			ws.Close()
			return
		} else {
			msg = string(p)
			event := &privateResponse{}
			err = json.Unmarshal([]byte(msg), &event)
			if err != nil {
				// received data update
				var data []interface{}
				err = json.Unmarshal([]byte(msg), &data)
				if err == nil {
					dataTerm := data[1].(string)
					dataList := data[2].([]interface{})

					// check for empty data
					if len(dataList) > 0 {
						if reflect.TypeOf(dataList[0]) == reflect.TypeOf([]interface{}{}) {
							// received list of lists
							for _, v := range dataList {
								ch <- TermData{
									Term: dataTerm,
									Data: v.([]interface{}),
								}
							}
						} else {
							// received flat list
							ch <- TermData{
								Term: dataTerm,
								Data: dataList,
							}
						}
					}
				}
			} else {
				// received auth response
				if event.Event == "auth" && event.Status != "OK" {
					ch <- TermData{
						Error: "Error connecting to private web socket channel.",
					}
					ws.Close()
				}
			}
		}
	}
}
