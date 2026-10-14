package main

import (
	"encoding/hex"
	"encoding/json"
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

type AppState struct {
	lock        sync.Mutex
	isAwakened  bool
	bootEntryId *string
	expiresAt   time.Time // Время истечения TTL
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

	bootEntryIdPattern = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`) // a-z, A-Z, 0-9, -, _
)

func main() {
	flag.StringVar(&targetMACAddr, "mac-addr", "AA:BB:CC:DD:EE:FF", "Target MAC address")
	flag.StringVar(&targetWOLAddr, "wol-ip-addr", "255.255.255.255", "Target broadcast IP address")
	flag.UintVar(&targetWOLPort, "wol-port", 9, "Target WoL port")
	flag.DurationVar(&ttl, "ttl", 2*time.Minute, "Awakening info TTL (e.g.: 90s, 10m)")
	bindHost := flag.String("host", "0.0.0.0", "Server bind host")
	bindPort := flag.String("port", "9009", "Server bind port")
	flag.Parse()

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
		log.Fatalf("Ошибка запуска сервера: %v", err)
	}
}

func handleWakeUp(res http.ResponseWriter, req *http.Request) {
	var reqData WakeUpRequest

	// Проверяем валидность тела, если оно есть
	if req.Body != nil && req.ContentLength != 0 {
		defer req.Body.Close()
		if err := json.NewDecoder(io.LimitReader(req.Body, 1024)).Decode(&reqData); err != nil && err != io.EOF {
			http.Error(res, `{"error": "Invalid JSON body!"}`, http.StatusBadRequest)
			return
		}
	}

	// Валидируем boot_entry_id, если он задан
	if reqData.BootEntryId != nil && *reqData.BootEntryId != "" {
		if !bootEntryIdPattern.MatchString(*reqData.BootEntryId) {
			http.Error(res, `{"error": "Field 'boot_entry_id' contains forbidden chars. (allowed: a-z, A-Z, 0-9, _, -)"}`, http.StatusBadRequest)
			return
		}
	}

	// Пытаемся разбудить комп через 'Wake on LAN'
	targetAddr := fmt.Sprintf("%s:%v", targetWOLAddr, targetWOLPort)
	if err := awake(targetMACAddr, targetAddr); err != nil {
		log.Printf("Failed to send WoL Magic Packet: %v", err)
		http.Error(res, fmt.Sprintf(`{"error": "Failed to send WoL Magic Packet: %v"}`, err), http.StatusInternalServerError)
		return
	}

	// Лочим состояние
	state.lock.Lock()

	// Записываем переданные данные в состояние
	state.isAwakened = true
	if reqData.BootEntryId != nil && *reqData.BootEntryId != "" {
		val := *reqData.BootEntryId
		state.bootEntryId = &val
	} else {
		state.bootEntryId = nil
	}

	// Указываем время истечения
	state.expiresAt = time.Now().Add(ttl)

	// Снимаем лок
	state.lock.Unlock()

	log.Printf("Sent WoL Magic Packet to %s for %s, (boot_entry_id: %v, expires_at: %s)",
		targetWOLAddr, targetMACAddr, state.bootEntryId, state.expiresAt.Format(time.RFC3339))

	// Шлем HTTP ответ
	res.Header().Set("Content-Type", "application/json")
	res.WriteHeader(http.StatusOK)
	res.Write([]byte(`{"message": "WoL Magic Packet sent!"}`))
}

func handleAwakeningInfo(res http.ResponseWriter, req *http.Request) {
	// Достаем query параметры
	reset := req.URL.Query().Get("reset") == "true"
	format := req.URL.Query().Get("format")

	// Лочим состояние
	state.lock.Lock()

	// Сбрасываем состояние, если истек TTL
	if state.isAwakened && time.Now().After(state.expiresAt) {
		state.isAwakened = false
		state.bootEntryId = nil
	}

	// Формируем ответ
	resp := AwakeningInfoResponse{
		IsAwakened:  state.isAwakened,
		BootEntryId: state.bootEntryId,
	}

	// Сбрасываем состояние, если был передал флаг сброса
	if reset {
		state.isAwakened = false
		state.bootEntryId = nil
		state.expiresAt = time.Time{}
	}

	// Снимаем лок
	state.lock.Unlock()

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

	// Проверяем не задан ли левак в параметре format
	if format != "" && format != "json" {
		http.Error(res, `{"error": "Parameter 'format' supports only 'json' or 'grub-vars'"}`, http.StatusInternalServerError)
	}

	// Вовзращаем JSON по дефолту
	res.Header().Set("Content-Type", "application/json")
	json.NewEncoder(res).Encode(resp)
}

func awake(macAddr string, ipAddr string) error {
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
		return fmt.Errorf("invalid MAC-address: %s", macAddr)
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
