package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

var ErrAlreadyAwakened = errors.New("already awakened, wait for TTL expiration or reset")

type AppState struct {
	lock        sync.Mutex
	isAwakened  bool
	bootEntryId *string
	expiresAt   time.Time // Время истечения TTL
}

func (s *AppState) TryWakeUp(bootEntryId *string, ttl time.Duration) (time.Time, error) {
	s.lock.Lock()
	defer s.lock.Unlock()

	now := time.Now()
	if s.isAwakened && now.Before(s.expiresAt) {
		return s.expiresAt, ErrAlreadyAwakened
	}

	// Записываем переданные данные в состояние
	s.isAwakened = true
	if bootEntryId != nil && *bootEntryId != "" {
		val := *bootEntryId
		s.bootEntryId = &val
	} else {
		s.bootEntryId = nil
	}

	// Указываем время истечения
	s.expiresAt = now.Add(ttl)

	return s.expiresAt, nil
}

func (s *AppState) GetInfo(reset bool) AwakeningInfoResponse {
	s.lock.Lock()
	defer s.lock.Unlock()

	// Сбрасываем состояние, если истек TTL
	if s.isAwakened && time.Now().After(s.expiresAt) {
		log.Printf("State reset! TTL expired at %s", s.expiresAt.Format(time.RFC3339))
		s.resetUnlocked()
	}

	var bootIdCopy *string
	if s.bootEntryId != nil {
		val := *s.bootEntryId
		bootIdCopy = &val
	}

	// Формируем ответ
	resp := AwakeningInfoResponse{
		IsAwakened:  s.isAwakened,
		BootEntryId: bootIdCopy,
	}

	// Сбрасываем состояние, если был передал флаг сброса
	if reset {
		s.resetUnlocked()
	}

	return resp
}

func (s *AppState) Reset() {
	s.lock.Lock()
	defer s.lock.Unlock()
	s.resetUnlocked()
}

func (s *AppState) resetUnlocked() {
	s.isAwakened = false
	s.bootEntryId = nil
	s.expiresAt = time.Time{}
}

type WakeUpRequest struct {
	BootEntryId *string `json:"boot_entry_id"`
}

type AwakeningInfoResponse struct {
	IsAwakened  bool    `json:"is_awakened"`
	BootEntryId *string `json:"boot_entry_id"`
}

var (
	// Глобальное состояние
	state AppState

	// Целевой IP-адрес (самого компа или в некоторых
	// случаях прокатывает бродкастинг на 255.255.255.255)
	targetWOLAddr string

	// Порт WoL (чаще всего 9-ка, но вроде как еще встречаются 7 или 4343)
	targetWOLPort uint

	// Целевой MAC-адрес (сетевого адаптера запускаемого компа)
	targetMACAddr string

	// Время жизни инфы о пробуждении
	ttl time.Duration

	bootEntryIdPattern = regexp.MustCompile(`^[a-zA-Z0-9_\-.]+$`) // a-z, A-Z, 0-9, -, _ и точки (.)
)

func main() {
	flag.StringVar(&targetMACAddr, "mac-addr", "AA:BB:CC:DD:EE:FF", "Target MAC address")
	flag.StringVar(&targetWOLAddr, "wol-ip-addr", "255.255.255.255", "Target broadcast IP address")
	flag.UintVar(&targetWOLPort, "wol-port", 9, "Target WoL port")
	flag.DurationVar(&ttl, "ttl", 2*time.Minute, "Awakening info TTL (e.g.: 90s, 10m)")
	bindHost := flag.String("host", "0.0.0.0", "Server bind host")
	bindPort := flag.String("port", "9009", "Server bind port")
	flag.Parse()

	if _, err := parseMAC(targetMACAddr); err != nil {
		log.Fatalf("Invalid MAC address (%s): %v", targetMACAddr, err)
	}

	bindAddr := fmt.Sprintf("%s:%s", *bindHost, *bindPort)

	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/wake-up", handleWakeUp)
	mux.HandleFunc("GET /v1/awakening-info", handleAwakeningInfo)

	log.Println("===== [GRUB LAN Awakener] ====")
	log.Printf("- MAC Addr: %s", targetMACAddr)
	log.Printf("- WoL IP Addr: %s", targetWOLAddr)
	log.Printf("- WoL Port: %v", targetWOLPort)
	log.Println()
	log.Printf("Server started on %s", bindAddr)
	if err := http.ListenAndServe(bindAddr, mux); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}

func handleWakeUp(res http.ResponseWriter, req *http.Request) {
	var reqData WakeUpRequest

	// Проверяем валидность тела, если оно есть
	if req.Body != nil {
		defer req.Body.Close()
		if err := json.NewDecoder(io.LimitReader(req.Body, 1024)).Decode(&reqData); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(res, "Invalid JSON body!", http.StatusBadRequest)
			return
		}
	}

	// Валидируем boot_entry_id, если он задан
	if reqData.BootEntryId != nil && *reqData.BootEntryId != "" {
		if !bootEntryIdPattern.MatchString(*reqData.BootEntryId) {
			writeJSONError(res, "Field 'boot_entry_id' contains forbidden chars. (allowed: a-z, A-Z, 0-9, _, -)", http.StatusBadRequest)
			return
		}
	}

	expiresAt, err := state.TryWakeUp(reqData.BootEntryId, ttl)
	if errors.Is(err, ErrAlreadyAwakened) {
		msg := fmt.Sprintf("Already awakened! Wait until %s or call reset.", expiresAt.Format(time.RFC3339))
		writeJSONError(res, msg, http.StatusConflict)
		return
	}

	// Пытаемся разбудить комп через 'Wake on LAN'
	targetAddr := fmt.Sprintf("%s:%v", targetWOLAddr, targetWOLPort)
	if err := awake(targetMACAddr, targetAddr); err != nil {
		state.Reset()
		log.Printf("Failed to send WoL Magic Packet: %v", err)
		writeJSONError(res, fmt.Sprintf("Failed to send WoL Magic Packet: %v", err), http.StatusInternalServerError)
		return
	}

	bootEntryLog := "None"
	if reqData.BootEntryId != nil && *reqData.BootEntryId != "" {
		bootEntryLog = *reqData.BootEntryId
	}

	msg := fmt.Sprintf("Sent WoL Magic Packet to %s for %s, (boot_entry_id: %s, expires_at: %s)",
		targetWOLAddr, targetMACAddr, bootEntryLog, expiresAt.Format(time.RFC3339))
	log.Print(msg)

	// Шлем HTTP ответ
	writeJSON(res, http.StatusOK, map[string]string{
		"message": msg,
	})
}

func handleAwakeningInfo(res http.ResponseWriter, req *http.Request) {
	// Достаем query параметры
	reset := req.URL.Query().Get("reset") == "true"
	format := req.URL.Query().Get("format")

	// Проверяем не задан ли левак в параметре format
	if format != "" && format != "json" && format != "grub-vars" {
		writeJSONError(res, "Parameter 'format' supports only 'json' or 'grub-vars'", http.StatusBadRequest)
		return
	}

	resp := state.GetInfo(reset)

	// Формируем читаемый лог
	bootEntryLog := "NOT_SET"
	if resp.BootEntryId != nil && *resp.BootEntryId != "" {
		bootEntryLog = *resp.BootEntryId
	}

	formatLog := format
	if formatLog == "" {
		formatLog = "json"
	}

	if reset {
		log.Printf("Awakening info read with reset by %s (reset: %t, format: %s, is_awakened: %t, boot_entry_id: %s)",
			req.RemoteAddr, reset, formatLog, resp.IsAwakened, bootEntryLog)
	} else {
		log.Printf("Awakening info read by %s (is_awakened: %t, boot_entry_id: %s, format: %s)",
			req.RemoteAddr, resp.IsAwakened, bootEntryLog, formatLog)
	}

	// Возвращаем ответ в формате установки grub переменных
	// Дабы не ебаться с парсингом JSON-а, а просто задать их через source (...)
	if format == "grub-vars" {
		res.Header().Set("Content-Type", "text/plain; charset=utf-8")
		res.WriteHeader(http.StatusOK)
		if resp.IsAwakened {
			fmt.Fprintln(res, `set is_awakened="true"`)
		} else {
			fmt.Fprintln(res, `set is_awakened="false"`)
		}
		if resp.BootEntryId != nil {
			fmt.Fprintf(res, "set boot_entry_id=\"%s\"\n", *resp.BootEntryId)
		} else {
			fmt.Fprintln(res, `set boot_entry_id=""`)
		}
		return
	}

	// Вовзращаем JSON по дефолту
	writeJSON(res, http.StatusOK, resp)
}

func parseMAC(macAddr string) ([]byte, error) {
	// Чистим MAC-адрес от всякой шняги (лишних :, - и .)
	mac := strings.ReplaceAll(macAddr, "-", "")
	mac = strings.ReplaceAll(mac, ":", "")
	mac = strings.ReplaceAll(mac, ".", "")

	// Сейчас почищеная строка может имееть вид: 'Abc123deF456'
	// Регистр не имеет значения, т.к. каждые два символа в ней -
	// это байт в шестнадцатиричном виде.
	// Переводим ее из "abc123def456" к виду [0xAB, 0xC1, 0x23, 0xDE, 0xF4, 0x56]
	// (всего - 6 байт)
	// --------------------------------------------------------------------------
	// Юзаем функцию из hex и через нее делаем то что было описано выше
	// и чекаем размер выходного массива
	macBytes, err := hex.DecodeString(mac)
	if err != nil || len(macBytes) != 6 {
		return nil, fmt.Errorf("invalid MAC-address: %s", macAddr)
	}
	return macBytes, nil
}

func awake(macAddr string, ipAddr string) error {
	macBytes, err := parseMAC(macAddr)
	if err != nil {
		return err
	}

	// Собираем так называемый магический пакет (Magic Packet)
	// Представляет из себя 0xFF (1 байт) * 6 (раз) + macBytes (6 байт) * 16 (раз)
	// Общий размер: 6 + 16 * 6 = 102 байта
	packet := make([]byte, 102)
	for i := range 6 {
		packet[i] = 0xFF
	}
	for i := 1; i <= 16; i++ {
		copy(packet[i*6:], macBytes)
	}

	// Открываем UDP соединение с заданым IP-адресом
	conn, err := net.Dial("udp", ipAddr)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Шлем в это соединение пакет пробуждения
	_, err = conn.Write(packet)
	return err
}

func writeJSON(res http.ResponseWriter, status int, data any) {
	res.Header().Set("Content-Type", "application/json; charset=utf-8")
	res.WriteHeader(status)
	_ = json.NewEncoder(res).Encode(data)
}

func writeJSONError(res http.ResponseWriter, message string, status int) {
	writeJSON(res, status, map[string]string{"error": message})
}
