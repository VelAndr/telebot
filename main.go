package main

// telebot
import (
	"csvdb"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/go-telegram-bot-api/telegram-bot-api"
	"github.com/vaughan0/go-ini"
	"golang.org/x/net/proxy"
)

type sendMess struct { // Элемент хеша с ключом по MAC-адресу
	chId               int64
	userName, textMess string
}

type jsonRes struct {
	Data       string `json:"Data"`
	LastUpdate string `json:"LastUpdate"`
	Name       string `json:"Name"`
}

type jsonResp struct {
	ServerTime string    `json:"ServerTime"`
	Result     []jsonRes `json:"result"`
}

var sendChan = make(chan sendMess)
var cfgCmd = make(map[string]string)
var cfgToken, cfgDomUrl, cfgPassWord, cfgEraseWord, cfgDbFile, cfgBindAddr, cfgHttpPort string
var cfgProxyAddr, cfgProxyPort, cfgProxyUser, cfgProxyPassword string
var cfgEnDomControl, cfgEnableProxy bool
var Bot *tgbotapi.BotAPI
var conf ini.File

func loadMainConfig() {
	var ok bool
	var err error
	conf, err = ini.LoadFile("telebot.ini")
	if err != nil {
		panic("Ошибка чтения конфигурационного файла!")
	}
	cfgEnableProxy = false
	t, ok := conf.Get("GLOBAL", "Proxy")
	if ok && t == "SOCKS5" {
		cfgEnableProxy = true
		cfgProxyAddr, ok = conf.Get("GLOBAL", "ProxyAddr")
		if !ok {
			panic("Не указан адрес прокси-сервера!")
		}
		cfgProxyPort, ok = conf.Get("GLOBAL", "ProxyPort")
		if !ok {
			panic("Не указан порт прокси-сервера!")
		}
		cfgProxyUser, ok = conf.Get("GLOBAL", "ProxyUser")
		if !ok {
			panic("Не указан пользователь прокси-сервера!")
		}
		cfgProxyPassword, ok = conf.Get("GLOBAL", "ProxyPassword")
		if !ok {
			panic("Ошибка! Не указан пароль пользователя!")
		}

	}
	cfgDbFile, ok = conf.Get("GLOBAL", "DBfile")
	if !ok {
		cfgDbFile = "telebot.dat"
	}
	cfgDbFile = filepath.ToSlash(cfgDbFile)

	cfgEnDomControl = true
	cfgDomUrl, ok = conf.Get("GLOBAL", "DomURL")
	if !ok {
		cfgEnDomControl = false
	}
	cfgToken, ok = conf.Get("GLOBAL", "Token")
	if !ok {
		panic("Ошибка! Должен быть указан токен чатбота")
	}
	cfgPassWord, ok = conf.Get("GLOBAL", "PassWord")
	if !ok {
		panic("Ошибка! Должен быть указан пароль для авторизации пользователя")
	}
	cfgEraseWord, ok = conf.Get("GLOBAL", "EraseWord")
	if !ok {
		cfgEraseWord = "EraseAll"
	}
	cfgBindAddr, ok = conf.Get("GLOBAL", "HTTPbindaddr")
	if !ok {
		cfgBindAddr = "127.0.0.1"
	}
	cfgHttpPort, ok = conf.Get("GLOBAL", "PORT")
	if !ok {
		cfgHttpPort = "9999"
	} else {
		t, err := strconv.Atoi(cfgHttpPort)
		if err != nil || t < 1024 || t > 65534 {
			t = 9999
		}
		cfgHttpPort = strconv.Itoa(t)
	}
}

func loadCmdConfig() {
	var err error
	conf, err = ini.LoadFile("telebot.ini")
	if err != nil {
		panic("Ошибка чтения конфигурационного файла!")
	}
	cfgCmd = conf.Section("COMMAND")
}

func printConfig() {
	fmt.Println("Текущая конфигурация:")
	fmt.Println("Файл базы данных - ", cfgDbFile)
	fmt.Println("Токен - ", cfgToken)
	fmt.Println("Пароли авторизации/удаления - ", cfgPassWord+"/"+cfgEraseWord)
	fmt.Println("HTTP сервер - ", cfgBindAddr+":"+cfgHttpPort)
	if cfgEnDomControl {
		fmt.Println("Domoticz url - ", cfgDomUrl)
		fmt.Println(" - Команды - ")
		for k, v := range cfgCmd {
			fmt.Println("слово:", k, ", команда:", v)
		}
	}
}

func getNotify(w http.ResponseWriter, req *http.Request) {
	req.ParseForm()
	str := ""
	subj := req.Form.Get("subj")
	if subj != "" {
		str = subj + ":"
	}
	str = str + req.Form.Get("mess")
	for k := range csvdb.DB { // проход по авторизованным пользователям
		tgSend(k, str)
	}
}

func tgRecv() {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates, _ := Bot.GetUpdatesChan(u)
	for update := range updates {
		if update.Message == nil {
			continue
		}
		sendChan <- sendMess{update.Message.Chat.ID, update.Message.From.UserName, update.Message.Text}
	}
}

func tgSend(chid, mess string) {
	tch, _ := strconv.ParseInt(chid, 10, 0)
	tgMess := tgbotapi.NewMessage(tch, mess)
	tgMess.ParseMode = tgbotapi.ModeMarkdown
	Bot.Send(tgMess)
}

func getDomValue(idx string) string { // получаем и формируем строку из ответа домотикса
	var res jsonResp
	retstr := ""

	if idx == "-" {
		retstr = "------------------------------\n"
	} else {
		dev, _ := strconv.Atoi(idx) // Дополнительная проверка на валидность
		resp, err := http.Get(cfgDomUrl + "/json.htm?type=devices&rid=" + strconv.Itoa(dev))
		body, err := ioutil.ReadAll(resp.Body)
		resp.Body.Close()

		err = json.Unmarshal(body, &res)
		if err != nil {
			fmt.Println(err)
		} else {
			if res.Result != nil {
				retstr = res.Result[0].Name + ": *" + res.Result[0].Data + "*  (_" + res.Result[0].LastUpdate + "_)\n"
			} else {
				retstr = "! - Invalid IDX - " + idx
			}
		}
	}
	return retstr
}

func setDomValue(idx, strcmd string) { // выполняем команду включения свитча или селектора
	dev, _ := strconv.Atoi(idx) // Дополнительная проверка на валидность
	http.Get(cfgDomUrl + "/json.htm?type=command&param=switchlight&idx=" + strconv.Itoa(dev) + "&" + strcmd)
}

func execCmd(cmd string) string { // выполняет команду и возвращает строку для отправки
	// G - получить значения, S - установить, L - вернуть список пользователей
	res := ""
	switch cmd[0] {
	case 'G':
		idxs := strings.Split(cmd[1:], ",")
		for i := range idxs {
			res = res + getDomValue(idxs[i])
		}
	case 'S':
		/// selector - json.htm?type=command&param=switchlight&idx=40&switchcmd=Set Level&level=0
		// switch - json.htm?type=command&param=switchlight&idx=99&switchcmd=Off
		idxs := strings.Split(cmd[1:], ",")
		setDomValue(idxs[0], idxs[1])
		res = "Сделано!"
	case 'R':
		loadCmdConfig()
		res = "Сделано!"
	case 'H': // help со списком команд
		res = "Список команд:\n"
		for k := range cfgCmd {
			res = res + "*" + k + "* - " + cfgCmd[k] + "\n"
		}
	case 'L':
		for k, v := range csvdb.DB {
			res = res + "User:*" + v[0] + "*, chatId:*" + k + "*\n"
		}
	}
	return res
}

func manageMess() {
	var chid, user, mess string

	for {
		dat := <-sendChan
		chid = strconv.FormatInt(dat.chId, 10)
		user = dat.userName
		mess = dat.textMess
		if csvdb.DB[chid] != nil { // Есть такой в базе данных - авторизация пройдена
			if mess == cfgEraseWord { // Стираем всех пользователей
				csvdb.Del("")
				tgSend(chid, user+", не забудьте повторно авторизоваться!")
			} else { // Выполняем команды из конфига
				for k := range cfgCmd { // проход по массиву команд
					if mess == k { // Есть такая команда
						tgSend(chid, execCmd(cfgCmd[k]))
					}
				}
			}
		} else { // Нет такого - принимаем только пароль
			if mess == cfgPassWord {
				//					_,_ = Bot.DeleteMessage()
				for k := range csvdb.DB {
					tgSend(k, "Внимание! В текущий чат добавлен пользователь *"+user+"*!")
				}
				csvdb.Add(chid, []string{user})
				tgSend(chid, "Добро пожаловать в чат, *"+user+"*")
			} else {
				tgSend(chid, "Ты кто такой, *"+user+"*, давай, до свидания!")
			}
		}
	}
}

func main() {
	var err error

	loadMainConfig()
	loadCmdConfig()
	//	printConfig()
	csvdb.Init(cfgDbFile)

	if cfgEnableProxy {
		proxy_addr := cfgProxyAddr + ":" + cfgProxyPort
		myAuth := proxy.Auth{User: cfgProxyUser, Password: cfgProxyPassword}
		dialSocksProxy, err := proxy.SOCKS5("tcp", proxy_addr, &myAuth, proxy.Direct)
		if err != nil {
			fmt.Println("Error connecting to proxy:", err)
		}
		tr := &http.Transport{Dial: dialSocksProxy.Dial}
		Bot, err = tgbotapi.NewBotAPIWithClient(cfgToken, &http.Client{Transport: tr})
	} else {
		Bot, err = tgbotapi.NewBotAPI(cfgToken)
	}
	if err != nil {
		panic("Ошибка коннекта c чатботом, возможен неправильный токен!")
	}
	Bot.Debug = false
	//	log.Printf("Authorized on account %s", Bot.Self.UserName)

	go tgRecv()
	go manageMess()
	http.HandleFunc("/notify", getNotify)
	log.Fatal(http.ListenAndServe(cfgBindAddr+":"+cfgHttpPort, nil))
}
