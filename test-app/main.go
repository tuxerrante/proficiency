package main

import (
	"encoding/json"
	"log"
	"math/rand"
	"net/http"
	_ "net/http/pprof"
	"strconv"
	"time"
)

// @title           Pet Store API
// @version         1.0
// @description     A simple pet store service for testing proficiency
// @host      localhost:6060
// @BasePath  /
// @schemes http
func main() {
	rand.Seed(time.Now().UnixNano())

	pets[1] = Pet{ID: 1, Name: "Fido", Tag: "dog"}
	pets[2] = Pet{ID: 2, Name: "Whiskers", Tag: "cat"}
	nextID = 3

	http.HandleFunc("/pets", petsHandler)
	http.HandleFunc("/pets/", petByIDHandler)
	http.HandleFunc("/health", healthHandler)

	log.Println("Starting test service on :6060")
	log.Fatal(http.ListenAndServe(":6060", nil))
}

type Pet struct {
	ID   int    `json:"id" example:"1"`
	Name string `json:"name" example:"Fido"`
	Tag  string `json:"tag,omitempty" example:"dog"`
}

var pets = make(map[int]Pet)
var nextID = 1

// ListPets godoc
// @Summary      List all pets
// @Description  Get a list of all pets in the store
// @Tags         pets
// @Produce      json
// @Success      200  {array}   Pet
// @Router       /pets [get]
func listPets(w http.ResponseWriter, r *http.Request) {
	simulateWork(10 * time.Millisecond)

	petList := make([]Pet, 0, len(pets))
	for _, pet := range pets {
		petList = append(petList, pet)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(petList)
}

// CreatePet godoc
// @Summary      Create a pet
// @Description  Add a new pet to the store
// @Tags         pets
// @Accept       json
// @Produce      json
// @Param        pet  body      Pet  true  "Pet to add"
// @Success      201  {object}  Pet
// @Failure      400  {string}  string "Invalid request"
// @Router       /pets [post]
func createPet(w http.ResponseWriter, r *http.Request) {
	var pet Pet
	if err := json.NewDecoder(r.Body).Decode(&pet); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	simulateMemoryWork()

	pet.ID = nextID
	nextID++
	pets[pet.ID] = pet

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	json.NewEncoder(w).Encode(pet)
}

func petsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		listPets(w, r)
	case "POST":
		createPet(w, r)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// GetPet godoc
// @Summary      Get a pet by ID
// @Description  Get details of a specific pet
// @Tags         pets
// @Produce      json
// @Param        id   path      int  true  "Pet ID"
// @Success      200  {object}  Pet
// @Failure      404  {string}  string "Pet not found"
// @Router       /pets/{id} [get]
func getPet(w http.ResponseWriter, r *http.Request, id int) {
	simulateWork(5 * time.Millisecond)

	pet, ok := pets[id]
	if !ok {
		http.Error(w, "Pet not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(pet)
}

// DeletePet godoc
// @Summary      Delete a pet
// @Description  Remove a pet from the store
// @Tags         pets
// @Param        id   path      int  true  "Pet ID"
// @Success      204  "No Content"
// @Failure      404  {string}  string "Pet not found"
// @Router       /pets/{id} [delete]
func deletePet(w http.ResponseWriter, r *http.Request, id int) {
	if _, ok := pets[id]; !ok {
		http.Error(w, "Pet not found", http.StatusNotFound)
		return
	}

	delete(pets, id)
	w.WriteHeader(http.StatusNoContent)
}

func petByIDHandler(w http.ResponseWriter, r *http.Request) {
	idStr := r.URL.Path[len("/pets/"):]
	id, err := strconv.Atoi(idStr)
	if err != nil {
		http.Error(w, "Invalid pet ID", http.StatusBadRequest)
		return
	}

	switch r.Method {
	case "GET":
		getPet(w, r, id)
	case "DELETE":
		deletePet(w, r, id)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// HealthCheck godoc
// @Summary      Health check
// @Description  Check if service is running
// @Tags         health
// @Produce      json
// @Success      200  {object}  map[string]string
// @Router       /health [get]
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func simulateWork(duration time.Duration) {
	start := time.Now()
	for time.Since(start) < duration {
		_ = fibonacci(20)
	}
}

func fibonacci(n int) int {
	if n <= 1 {
		return n
	}
	return fibonacci(n-1) + fibonacci(n-2)
}

func simulateMemoryWork() {
	data := make([]byte, 1024*100)
	for i := range data {
		data[i] = byte(rand.Intn(256))
	}
}
