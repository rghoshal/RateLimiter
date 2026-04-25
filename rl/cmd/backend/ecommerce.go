package main

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ─── Data Models ──────────────────────────────────────────────────────────────

type Product struct {
	ID    int     `json:"id"`
	Name  string  `json:"name"`
	Price float64 `json:"price"`
	Stock int     `json:"stock"`
}

type CartItem struct {
	ProductID int `json:"product_id"`
	Quantity  int `json:"quantity"`
}

type Order struct {
	ID        string     `json:"id"`
	UserID    string     `json:"user_id"`
	Items     []CartItem `json:"items"`
	Total     float64    `json:"total"`
	CreatedAt time.Time  `json:"created_at"`
	Status    string     `json:"status"`
}

// ─── In-Memory Data Store ─────────────────────────────────────────────────────

type Store struct {
	mu       sync.RWMutex
	products map[int]Product
	carts    map[string][]CartItem // user_id -> cart items
	orders   map[string]Order      // order_id -> order
	orderID  int
}

func NewStore() *Store {
	s := &Store{
		products: make(map[int]Product),
		carts:    make(map[string][]CartItem),
		orders:   make(map[string]Order),
	}

	// Pre-populate products
	s.products[1] = Product{ID: 1, Name: "Laptop", Price: 999.99, Stock: 50}
	s.products[2] = Product{ID: 2, Name: "Mouse", Price: 29.99, Stock: 500}
	s.products[3] = Product{ID: 3, Name: "Keyboard", Price: 79.99, Stock: 300}
	s.products[4] = Product{ID: 4, Name: "Monitor", Price: 349.99, Stock: 100}
	s.products[5] = Product{ID: 5, Name: "Headphones", Price: 199.99, Stock: 200}

	return s
}

// ─── API Handlers ─────────────────────────────────────────────────────────────

func (s *Store) ListProducts(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	products := make([]Product, 0, len(s.products))
	for _, p := range s.products {
		products = append(products, p)
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Tier", "read")
	json.NewEncoder(w).Encode(map[string]any{
		"products": products,
		"count":    len(products),
	})
	slog.Info("Listed products", "count", len(products))
}

func (s *Store) GetProduct(w http.ResponseWriter, r *http.Request) {
	idStr := r.PathValue("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, `{"error":"invalid product id"}`, http.StatusBadRequest)
		return
	}

	s.mu.RLock()
	product, exists := s.products[id]
	s.mu.RUnlock()

	if !exists {
		http.Error(w, `{"error":"product not found"}`, http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Tier", "read")
	json.NewEncoder(w).Encode(product)
	slog.Info("Got product", "id", id, "name", product.Name)
}

func (s *Store) GetCart(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		http.Error(w, `{"error":"user id required"}`, http.StatusUnauthorized)
		return
	}

	s.mu.RLock()
	cart := s.carts[userID]
	s.mu.RUnlock()

	// Calculate total
	var total float64
	for _, item := range cart {
		if p, ok := s.products[item.ProductID]; ok {
			total += p.Price * float64(item.Quantity)
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Tier", "user")
	json.NewEncoder(w).Encode(map[string]any{
		"items": cart,
		"total": total,
	})
	slog.Info("Got cart", "user_id", userID, "items", len(cart))
}

func (s *Store) AddToCart(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		http.Error(w, `{"error":"user id required"}`, http.StatusUnauthorized)
		return
	}

	var item CartItem
	if err := json.NewDecoder(r.Body).Decode(&item); err != nil {
		http.Error(w, `{"error":"invalid payload"}`, http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	product, exists := s.products[item.ProductID]
	if !exists {
		s.mu.Unlock()
		http.Error(w, `{"error":"product not found"}`, http.StatusNotFound)
		return
	}

	if item.Quantity > product.Stock {
		s.mu.Unlock()
		http.Error(w, `{"error":"insufficient stock"}`, http.StatusConflict)
		return
	}

	s.carts[userID] = append(s.carts[userID], item)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Tier", "user")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(map[string]any{
		"message": "item added to cart",
		"item":    item,
	})
	slog.Info("Added to cart", "user_id", userID, "product_id", item.ProductID, "qty", item.Quantity)
}

func (s *Store) ClearCart(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		http.Error(w, `{"error":"user id required"}`, http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	delete(s.carts, userID)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Tier", "user")
	json.NewEncoder(w).Encode(map[string]any{"message": "cart cleared"})
	slog.Info("Cleared cart", "user_id", userID)
}

func (s *Store) Checkout(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		http.Error(w, `{"error":"user id required"}`, http.StatusUnauthorized)
		return
	}

	s.mu.Lock()
	cart, exists := s.carts[userID]
	if !exists || len(cart) == 0 {
		s.mu.Unlock()
		http.Error(w, `{"error":"cart is empty"}`, http.StatusConflict)
		return
	}

	// Validate stock and calculate total
	var total float64
	for _, item := range cart {
		product, ok := s.products[item.ProductID]
		if !ok || product.Stock < item.Quantity {
			s.mu.Unlock()
			http.Error(w, `{"error":"item not available"}`, http.StatusConflict)
			return
		}
		total += product.Price * float64(item.Quantity)

		// Reduce stock
		product.Stock -= item.Quantity
		s.products[item.ProductID] = product
	}

	// Create order
	s.orderID++
	order := Order{
		ID:        "ORD-" + strconv.Itoa(s.orderID),
		UserID:    userID,
		Items:     cart,
		Total:     total,
		CreatedAt: time.Now(),
		Status:    "completed",
	}
	s.orders[order.ID] = order

	// Clear cart
	delete(s.carts, userID)
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Tier", "user")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(order)
	slog.Info("Checkout completed", "user_id", userID, "order_id", order.ID, "total", total)
}

func (s *Store) GetOrders(w http.ResponseWriter, r *http.Request) {
	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		http.Error(w, `{"error":"user id required"}`, http.StatusUnauthorized)
		return
	}

	s.mu.RLock()
	var userOrders []Order
	for _, order := range s.orders {
		if order.UserID == userID {
			userOrders = append(userOrders, order)
		}
	}
	s.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-RateLimit-Tier", "user")
	json.NewEncoder(w).Encode(map[string]any{
		"orders": userOrders,
		"count":  len(userOrders),
	})
	slog.Info("Got orders", "user_id", userID, "count", len(userOrders))
}

// Health check
func (s *Store) Health(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{
		"status":    "healthy",
		"timestamp": time.Now(),
	})
}
