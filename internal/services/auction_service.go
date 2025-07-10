package services

import (
	"context"
	"errors"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

type MessageKind int

const (
	PlaceBid MessageKind = iota

	SuccessfullyPlacedBid

	NewBidPlaced

	AuctionFinished
	// Errors
	FailedToPlaceBid
)

type Message struct {
	Message string
	Amount  float64
	Kind    MessageKind
	UserId  uuid.UUID
}

type AuctionLobby struct {
	sync.Mutex
	Rooms map[uuid.UUID]*AuctionRoom
}
type AuctionRoom struct {
	Id         uuid.UUID
	Context    context.Context
	Broadcast  chan Message
	Register   chan *Client
	Unregister chan *Client
	Clients    map[uuid.UUID]*Client

	BidsService BidsService
}

func (r *AuctionRoom) registerClient(c *Client) {
	slog.Info("New user Connected", "Client", c)
	r.Clients[c.UserId] = c
}

func (r *AuctionRoom) unregisterClient(c *Client) {
	slog.Info("User disconnected", "Client", c)
	delete(r.Clients, c.UserId)
}

func (r *AuctionRoom) broadcastMessage(m Message) {
	slog.Info("New message received", "RoomID", r.Id, "message", m.Message, "user_id", m.UserId)
	switch m.Kind {
	case PlaceBid:
		bid, err := r.BidsService.Placebid(r.Context, r.Id, m.UserId, m.Amount)
		if err != nil {
			if errors.Is(err, ErrBidIsTooLow) {
				if client, ok := r.Clients[m.UserId]; ok {
					client.Send <- Message{
						Message: ErrBidIsTooLow.Error(),
						Kind:    FailedToPlaceBid,
					}
				}
				return
			}
		}

		if client, ok := r.Clients[m.UserId]; ok {
			client.Send <- Message{Kind: SuccessfullyPlacedBid, Message: "Your bid was successfully placed"}
		}

		for id, client := range r.Clients {
			newBidMessage := Message{Kind: NewBidPlaced, Message: "A new cid was placed", Amount: bid.BidAmount}
			if id == m.UserId {
				continue
			}
			client.Send <- newBidMessage
		}
	}
}

func (r *AuctionRoom) Run() {
	slog.Info("Auction has started", "auctionId", r.Id)
	defer func() {
		close(r.Broadcast)
		close(r.Register)
		close(r.Unregister)
	}()

	for {
		select {
		case client := <-r.Register:
			r.registerClient(client)
		case client := <-r.Unregister:
			r.unregisterClient(client)
		case client := <-r.Broadcast:
			r.broadcastMessage(client)
		case <-r.Context.Done():
			slog.Info("Auction has ended.", "auctionID", r.Id)
			for _, client := range r.Clients {
				client.Send <- Message{Kind: AuctionFinished, Message: "Auction has been finished"}
			}
			return
		}
	}
}

func NewAuctionRoom(ctx context.Context, id uuid.UUID, BidsService BidsService) *AuctionRoom {
	return &AuctionRoom{
		Id:          id,
		Broadcast:   make(chan Message),
		Register:    make(chan *Client),
		Unregister:  make(chan *Client),
		Clients:     make(map[uuid.UUID]*Client),
		Context:     ctx,
		BidsService: BidsService,
	}
}

type Client struct {
	Room   *AuctionRoom
	Conn   *websocket.Conn
	Send   chan Message
	UserId uuid.UUID
}

func NewClient(room *AuctionRoom, conn *websocket.Conn, userId uuid.UUID) *Client {
	return &Client{
		Room:   room,
		Conn:   conn,
		Send:   make(chan Message, 512),
		UserId: userId,
	}
}
