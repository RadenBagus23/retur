package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gorilla/mux"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Struct Retur untuk merepresentasikan data retur yang ada di database
// Field-field di dalam struct sesuai dengan kolom yang ada di database
// Menggunakan tag JSON untuk pengubahan nama saat encoding/decoding
type Retur struct {
	ID          int    `json:"id"`         // ID unik untuk setiap retur
	Barang      string `json:"barang"`     // Nama barang yang diretur
	Alasan      string `json:"alasan"`     // Alasan pengembalian barang
	Status      string `json:"status"`     // Status retur (Dalam Proses, Disetujui, Tidak Disetujui)
	Pengembalian string `json:"pengembalian"` // Jenis pengembalian (barang atau uang)
}

// Stack adalah implementasi stack generik menggunakan slice
// Digunakan untuk menyimpan data yang dihapus dan bisa di-undo
type Stack[T any] struct {
	items []T // Slice untuk menyimpan item di dalam stack
}

// Push menambahkan item baru ke dalam stack
func (s *Stack[T]) Push(item T) {
	s.items = append(s.items, item)
}

// Pop menghapus item terakhir dari stack dan mengembalikannya
// Mengembalikan nilai kedua sebagai indikator apakah stack kosong
func (s *Stack[T]) Pop() (T, bool) {
	if len(s.items) == 0 {
		var zero T
		return zero, false // Jika stack kosong, mengembalikan nilai default dan false
	}
	item := s.items[len(s.items)-1]
	s.items = s.items[:len(s.items)-1] // Menghapus item terakhir dari stack
	return item, true
}

// IsEmpty memeriksa apakah stack kosong
func (s *Stack[T]) IsEmpty() bool {
	return len(s.items) == 0
}

// Variabel global untuk koneksi database dan stack yang menyimpan data yang dihapus
var (
	db           *gorm.DB          // Koneksi ke database
	deletedStack Stack[Retur]      // Stack untuk menyimpan data retur yang dihapus
	deletedIDs   []int             // Menyimpan ID barang yang dihapus untuk reuse ID
)

// initDB menginisialisasi koneksi ke database MySQL dan melakukan migrasi tabel Retur
func initDB() {
	var err error
	dsn := "root:@tcp(127.0.0.1:3306)/retur_db?charset=utf8mb4&parseTime=True&loc=Local" // Data Source Name untuk koneksi MySQL
	db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{}) // Membuka koneksi ke database
	if err != nil {
		panic("Failed to connect to database: " + err.Error()) // Keluar jika koneksi gagal
	}
	db.AutoMigrate(&Retur{}) // Melakukan migrasi tabel Retur di database
}

// respondJSON mengirimkan response JSON dengan status dan payload yang diberikan
func respondJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json") // Menetapkan header response sebagai JSON
	w.WriteHeader(status)                             // Menulis status HTTP
	json.NewEncoder(w).Encode(payload)                // Menyandikan payload menjadi JSON dan mengirimkan response
}

// handleError mengirimkan pesan error dalam format JSON
func handleError(w http.ResponseWriter, status int, message string) {
	respondJSON(w, status, map[string]string{"error": message}) // Mengirimkan pesan error dalam bentuk JSON
}

// getReturs adalah handler untuk mengambil semua data retur dari database
func getReturs(w http.ResponseWriter, r *http.Request) {
	var returs []Retur
	if err := db.Find(&returs).Error; err != nil {
		handleError(w, http.StatusInternalServerError, "Failed to retrieve returns") // Jika gagal mengambil data, kirim error
		return
	}
	respondJSON(w, http.StatusOK, returs) // Kirimkan data retur dalam format JSON
}

// createRetur adalah handler untuk membuat data retur baru di database
func createRetur(w http.ResponseWriter, r *http.Request) {
	var newRetur Retur
	if err := json.NewDecoder(r.Body).Decode(&newRetur); err != nil {
		handleError(w, http.StatusBadRequest, "Invalid input") // Jika input tidak valid, kirimkan error
		return
	}

	// Jika ada ID yang tersedia dari deletedIDs, gunakan kembali ID tersebut
	if len(deletedIDs) > 0 {
		newRetur.ID = deletedIDs[len(deletedIDs)-1] // Menggunakan ID yang telah dihapus sebelumnya
		deletedIDs = deletedIDs[:len(deletedIDs)-1]  // Hapus ID tersebut dari deletedIDs
	} else {
		var lastRetur Retur
		if err := db.Order("id desc").First(&lastRetur).Error; err == nil {
			newRetur.ID = lastRetur.ID + 1 // Jika ada retur sebelumnya, ID baru adalah ID terakhir + 1
		} else {
			newRetur.ID = 1 // Jika belum ada retur, mulai dengan ID 1
		}
	}

	newRetur.Status = "Dalam Proses" // Set status default menjadi "Dalam Proses"
	if err := db.Create(&newRetur).Error; err != nil {
		handleError(w, http.StatusInternalServerError, "Failed to create return") // Jika gagal membuat retur, kirimkan error
		return
	}
	respondJSON(w, http.StatusCreated, newRetur) // Kirimkan retur yang baru dibuat dalam format JSON
}

// approveReturHandler adalah handler untuk menyetujui retur dengan ID tertentu
func approveReturHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)              // Ambil parameter dari URL
	id, err := strconv.Atoi(vars["id"]) // Convert ID dari string ke integer
	if err != nil {
		handleError(w, http.StatusBadRequest, "Invalid ID format") // Jika format ID salah, kirimkan error
		return
	}

	var input struct {
		Pengembalian string `json:"pengembalian"` // Menyimpan input pengembalian (barang/uang)
	}
	if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
		handleError(w, http.StatusBadRequest, "Invalid input") // Jika input tidak valid, kirimkan error
		return
	}

	if input.Pengembalian != "barang" && input.Pengembalian != "uang" {
		handleError(w, http.StatusBadRequest, "Pengembalian must be 'barang' or 'uang'") // Validasi nilai pengembalian
		return
	}

	var retur Retur
	if err := db.First(&retur, id).Error; err != nil {
		handleError(w, http.StatusNotFound, "Return not found") // Jika retur tidak ditemukan, kirimkan error
		return
	}

	retur.Pengembalian = input.Pengembalian // Set pengembalian sesuai input
	retur.Status = "Disetujui"              // Set status menjadi "Disetujui"
	if err := db.Save(&retur).Error; err != nil {
		handleError(w, http.StatusInternalServerError, "Failed to update return") // Jika gagal memperbarui, kirimkan error
		return
	}
	respondJSON(w, http.StatusOK, retur) // Kirimkan retur yang sudah disetujui dalam format JSON
}

// disapproveReturHandler adalah handler untuk menolak retur dengan ID tertentu
func disapproveReturHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)              // Ambil parameter dari URL
	id, err := strconv.Atoi(vars["id"]) // Convert ID dari string ke integer
	if err != nil {
		handleError(w, http.StatusBadRequest, "Invalid ID format") // Jika format ID salah, kirimkan error
		return
	}

	var retur Retur
	if err := db.First(&retur, id).Error; err != nil {
		handleError(w, http.StatusNotFound, "Return not found") // Jika retur tidak ditemukan, kirimkan error
		return
	}

	retur.Status = "Tidak Disetujui" // Set status menjadi "Tidak Disetujui"
	if err := db.Save(&retur).Error; err != nil {
		handleError(w, http.StatusInternalServerError, "Failed to update return") // Jika gagal memperbarui, kirimkan error
		return
	}
	respondJSON(w, http.StatusOK, retur) // Kirimkan retur yang sudah ditolak dalam format JSON
}

// deleteReturHandler adalah handler untuk menghapus retur dengan ID tertentu
func deleteReturHandler(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)              // Ambil parameter dari URL
	id, err := strconv.Atoi(vars["id"]) // Convert ID dari string ke integer
	if err != nil {
		handleError(w, http.StatusBadRequest, "Invalid ID format") // Jika format ID salah, kirimkan error
		return
	}

	var retur Retur
	if err := db.First(&retur, id).Error; err != nil {
		handleError(w, http.StatusNotFound, "Return not found") // Jika retur tidak ditemukan, kirimkan error
		return
	}

	deletedIDs = append(deletedIDs, retur.ID) // Simpan ID yang dihapus untuk reuse
	deletedStack.Push(retur)                  // Push data yang dihapus ke stack
	if err := db.Delete(&retur).Error; err != nil {
		handleError(w, http.StatusInternalServerError, "Failed to delete return") // Jika gagal menghapus, kirimkan error
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"message": fmt.Sprintf("Return with ID %d deleted", id)}) // Kirimkan pesan bahwa retur telah dihapus
}

// undoDeleteReturHandler adalah handler untuk mengembalikan data retur yang terakhir dihapus
func undoDeleteReturHandler(w http.ResponseWriter, r *http.Request) {
	if deletedStack.IsEmpty() {
		handleError(w, http.StatusBadRequest, "No returns to undo") // Jika tidak ada retur yang dihapus, kirimkan error
		return
	}

	item, _ := deletedStack.Pop() // Pop item terakhir yang dihapus dari stack
	if err := db.Create(&item).Error; err != nil {
		handleError(w, http.StatusInternalServerError, "Failed to restore return") // Jika gagal mengembalikan retur, kirimkan error
		return
	}
	respondJSON(w, http.StatusOK, item) // Kirimkan retur yang sudah dikembalikan dalam format JSON
}

// main adalah fungsi utama untuk menjalankan server
func main() {
	initDB() // Inisialisasi koneksi database

	r := mux.NewRouter() // Membuat router baru
	// Menentukan endpoint dan handler yang sesuai
	r.HandleFunc("/retur", getReturs).Methods("GET")       // Endpoint untuk mengambil semua retur
	r.HandleFunc("/retur", createRetur).Methods("POST")    // Endpoint untuk membuat retur baru
	r.HandleFunc("/retur/{id}/approve", approveReturHandler).Methods("POST") // Endpoint untuk menyetujui retur
	r.HandleFunc("/retur/{id}/disapprove", disapproveReturHandler).Methods("POST") // Endpoint untuk menolak retur
	r.HandleFunc("/retur/{id}/delete", deleteReturHandler).Methods("DELETE") // Endpoint untuk menghapus retur
	r.HandleFunc("/retur/undo", undoDeleteReturHandler).Methods("POST") // Endpoint untuk mengembalikan retur yang dihapus

	http.ListenAndServe(":8080", r) // Menjalankan server di port 8080
}