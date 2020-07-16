package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/bwmarrin/discordgo"
)

var cfg configuration

var wg sync.WaitGroup

type configuration struct {
	BaseTime int64                `json:basetime`
	Discord  discordConfiguration `json:"discord"`
	/* Matrix  matrixConfiguration  `json:"matrix"` */
}

//TODO: add configuration option for selfbotting

type discordConfiguration struct {
	Token string `json:"token"`
	/* IsBot         bool   `json:"isBot"`*/
	TargetChannel string `json:"targetChannel"`
	TargetGuild   string `json:"targetGuild"`
	MuteRoleID    string `json:"muteRoleID"`
}

//TODO: Add Matrix configuration options

/*
type matrixConfiguration struct {
	Homeserver string `json:"homeserver"`,
	Username   string `json:"username"`,
	Password  string `json:"username"`
}
*/

func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	msg := m.Message
	if m.Author.ID != s.State.User.ID && !m.Author.Bot && m.ChannelID == cfg.Discord.TargetChannel {
		collides := findMessageCollisions(s, msg)
		if !collides {
			writeMessageHash(s, msg)
		} else {
			punishUser(s, msg)
		}
	}
}

func fetchBanTime(userID string) int64 {
	f, err := os.Open("./db/punishments/" + userID)
	if err != nil {
		log.Fatal("Could not open file to check punishments.")
	}
	scanner := bufio.NewScanner(f)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	timeWritten, _ := strconv.ParseInt(lines[1], 10, 64)
	return timeWritten
}

func punishUser(s *discordgo.Session, msg *discordgo.Message) {
	if _, err := os.Stat("./db/punishments/" + msg.Author.ID); os.IsNotExist(err) {
		f, err := os.OpenFile("./db/punishments/"+msg.Author.ID, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		time := strconv.FormatInt(time.Now().Unix(), 16)
		if _, err := f.Write([]byte("1" + "\n" + time)); err != nil {
			f.Close() // ignore error; Write error takes precedence
			log.Fatal(err)
		}
	} else {
		f, err := os.Open("./db/punishments/" + msg.Author.ID)
		if err != nil {
			log.Fatal(err)
		}
		scanner := bufio.NewScanner(f)
		var lines []string
		for scanner.Scan() {
			lines = append(lines, scanner.Text())
		}
		f.Close()

		writtenBanTick, _ := strconv.ParseInt(lines[0], 10, 64)
		newBanTick := writtenBanTick + 1
		newBanTime := (time.Now().Unix()) + (powerInt64(cfg.BaseTime, newBanTick))
		f2, err := os.OpenFile("./db/punishments/"+msg.Author.ID, os.O_WRONLY, 0644)
		if err != nil {
			log.Fatal(err)
		}
		if _, err := f2.Write([]byte(strconv.FormatInt(newBanTick, 10) + "\n" + strconv.FormatInt(newBanTime, 10))); err != nil {
			f2.Close() // ignore error; Write error takes precedence
			log.Fatal(err)
		}
		err = s.GuildMemberRoleAdd(msg.GuildID, msg.Author.ID, cfg.Discord.MuteRoleID)
		if err != nil {
			log.Fatal("Something went wrong with adding a role")
		}
		fmt.Println(newBanTime - (time.Now().Unix()))
		seconds := (newBanTime - time.Now().Unix())
		time.Sleep(time.Duration(seconds) * time.Second)
	}
	err := s.GuildMemberRoleRemove(msg.GuildID, msg.Author.ID, cfg.Discord.MuteRoleID)
	if err != nil {
		log.Fatal("Something went wrong with removing the role")
	}
}

func getFNV128Hash(text string) string {
	hasher := fnv.New128()
	_, err := hasher.Write([]byte(text))
	if err != nil {
		fmt.Println(err)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

func powerInt64(base int64, exponent int64) int64 {
	var output int64 = 1
	for exponent != 0 {
		output *= base
		exponent--
	}
	return output
}
func writeMessageHash(s *discordgo.Session, m *discordgo.Message) {
	hashedMessage := getFNV128Hash(m.Content)
	f, err := os.OpenFile("./db/hashes", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0740)
	if err != nil {
		fmt.Println(err)
	}
	if _, err := f.Write([]byte(hashedMessage + "\n")); err != nil {
		f.Close()
		fmt.Println(err)
	}
	if err := f.Close(); err != nil {
		fmt.Println(err)
	}
}

func findMessageCollisions(s *discordgo.Session, m *discordgo.Message) bool {
	if _, err := os.Stat("./db/hashes"); err == nil {
		file, err := os.Open("./db/hashes")
		if err != nil {
			fmt.Println(err)
			return false
		}
		scanner := bufio.NewScanner(file)
		hashedMessage := getFNV128Hash(m.Content)
		for scanner.Scan() {
			fmt.Println(scanner.Text())
			if hashedMessage == scanner.Text() {
				err := s.ChannelMessageDelete(m.ChannelID, m.ID)
				if err != nil {
					fmt.Println("Could not delete message")
				}
				return true
			}
		}

	}
	return false
}

func resumePunishment(userid string, banTime int64, s *discordgo.Session) {
	defer wg.Done()
	err := s.GuildMemberRoleAdd(cfg.Discord.TargetGuild, userid, cfg.Discord.MuteRoleID)
	if err != nil {
		fmt.Println(err)
	}
	toSleep := time.Duration(banTime - time.Now().Unix())
	time.Sleep(toSleep * time.Second)
	err = s.GuildMemberRoleRemove(cfg.Discord.TargetGuild, userid, cfg.Discord.MuteRoleID)
	if err != nil {
		fmt.Println(err)
	}
}

func removeRoleGoroutine(userid string, s *discordgo.Session) {
	defer wg.Done()
	err := s.GuildMemberRoleRemove(cfg.Discord.TargetGuild, userid, cfg.Discord.MuteRoleID)
	if err != nil {
		fmt.Println(err)
	}
}

func readyEvent(s *discordgo.Session, m *discordgo.Ready) {
	files, err := ioutil.ReadDir("./db/punishments")
	if err != nil {
		log.Fatal(err)
	}
	for _, f := range files {
		banTime := fetchBanTime(f.Name())
		if time.Now().Unix() < banTime {
			wg.Add(1)
			go resumePunishment(f.Name(), banTime, s)
		} else {
			wg.Add(1)
			go removeRoleGoroutine(f.Name(), s)
		}
	}
	wg.Wait()
}

func init() {
	fileReader, err := os.Open("config.json")
	if err != nil {
		log.Fatal(err)
	}
	defer fileReader.Close()
	fileBytes, err := ioutil.ReadAll(fileReader)
	if err != nil {
		log.Fatal(err)
	} else {
		json.Unmarshal(fileBytes, &cfg)
	}

	if _, err := os.Stat("db"); os.IsNotExist(err) {
		err = os.MkdirAll("db", 0740)
		if err != nil {
			log.Fatal(err)
		}
	}
	if _, err := os.Stat("db/punishmentsp"); os.IsNotExist(err) {
		err = os.MkdirAll("db/punishments", 0740)
		if err != nil {
			log.Fatal(err)
		}
	}
}

func main() {
	dg, err := discordgo.New("Bot " + cfg.Discord.Token)
	if err != nil {
		log.Fatal(err)
	}

	dg.AddHandler(messageCreate)
	dg.AddHandler(readyEvent)

	err = dg.Open()
	if err != nil {
		log.Fatal(err)
		return
	}

	sc := make(chan os.Signal, 1)
	signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, syscall.SIGTERM)
	<-sc
	dg.Close()
}
