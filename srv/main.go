package srv

import (
	"io/ioutil"
	"log"
	"net/http"
	"strings"
)

const helpMessage = `@telepyth\_bot is Telegram notifications in Python.

*Avaliable commands*:
/start begin interaction and issue new token.
/revoke revoke token issued before.
/last send currently valid token or nothing.
/help show help message and credentials.

See source code and more examples on [github page](https://github.com/daskol/telepyth).`

type TelePyth struct {
	Api     *TelegramBotApi
	Storage *Storage

	Polling bool
	Timeout int
}

func (t *TelePyth) HandleTelegramUpdate(update *Update) {
	log.Println("update from", update.Message.From.Id)

	switch update.Message.Text {
	case "/start":
		log.Println(update.Message.From.Id, "send /start")
		token, err := t.Storage.InsertUser(&update.Message.From)

		if err != nil {
			//  TODO: log error and ask try again
			log.Println(err)
			return
		}

		err = (&SendMessage{
			ChatId:    update.Message.From.Id,
			Text:      "Your access token is `" + token + "`.",
			ParseMode: "Markdown",
		}).To(t.Api)

		if err != nil {
			log.Println("error: ", err)
		}
	case "/last":
		log.Println(update.Message.From.Id, "send /last")
		token, err := t.Storage.SelectTokenBy(&update.Message.From)

		if err != nil {
			log.Println(err)
			return
		}

		if revoked, err := t.Storage.IsTokenRevokedBy(token); err != nil {
			log.Println("error: ", err)
		} else if revoked {
			err = (&SendMessage{
				ChatId: update.Message.From.Id,
				Text: "You do not have any valid token. " +
					"Send /start to issue new one.",
				ParseMode: "Markdown",
			}).To(t.Api)

			if err != nil {
				log.Println("error: ", err)
			}
		} else {
			err = (&SendMessage{
				ChatId:    update.Message.From.Id,
				Text:      "Your last valid token is `" + token + "`.",
				ParseMode: "Markdown",
			}).To(t.Api)

			if err != nil {
				log.Println("error: ", err)
			}
		}
	case "/revoke":
		log.Println(update.Message.From.Id, "send /revoke")

		if err := t.Storage.RevokeTokenBy(&update.Message.From); err != nil {
			log.Println("error:", err)
			return
		}

		err := (&SendMessage{
			ChatId: update.Message.From.Id,
			Text: "Token is already revoked. " +
				"Send /start to obtain new token.",
		}).To(t.Api)

		if err != nil {
			log.Println("error: ", err)
		}
	case "/help":
		log.Println(update.Message.From.Id, "send /help")
		err := (&SendMessage{
			ChatId:    update.Message.From.Id,
			Text:      helpMessage,
			ParseMode: "Markdown",
		}).To(t.Api)

		if err != nil {
			log.Println("error: ", err)
		}
	default:
		log.Println(update.Message.From.Id, "send unknown command")
		err := (&SendMessage{
			ChatId: update.Message.From.Id,
			Text:   "Unknown command. Try /help to see usage details.",
		}).To(t.Api)

		if err != nil {
			log.Println("error: ", err)
		}
	}
}

func (t *TelePyth) HandleWebhookRequest(w http.ResponseWriter, req *http.Request) {
	log.Println("HandleWebhookRequest(): not implemented!")
}

func (t *TelePyth) HandleNotifyRequest(w http.ResponseWriter, req *http.Request) {
	// validate request method
	if req.Method != "POST" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// check that content type is plain/text
	if contentType, ok := req.Header["Content-Type"]; !ok {
		w.WriteHeader(http.StatusBadRequest)
		return
	} else if contentType[0] == "plain/text" ||
		strings.HasPrefix(contentType[0], "plain/text; ") {
		// TODO: refactor this check
		// do nothing here
	} else {
		for k, v := range contentType {
			log.Println(k, v)
		}
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// split string to extract token
	token := strings.TrimPrefix(req.RequestURI, "/api/notify/")

	if len(token) == 0 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// is token valid
	if revoked, err := t.Storage.IsTokenRevokedBy(token); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	} else if revoked {
		w.WriteHeader(http.StatusUnauthorized)
	}

	// get user by token
	user, err := t.Storage.SelectUserBy(token)

	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	log.Println("user", user.Id, "notify with token", token)

	// extract message text
	bytes, err := ioutil.ReadAll(req.Body)

	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// send notification to user
	err = (&SendMessage{
		ChatId:    user.Id,
		Text:      string(bytes),
		ParseMode: "Markdown",
	}).To(t.Api)

	if err != nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
}

func (t *TelePyth) HandlePingRequest(w http.ResponseWriter, req *http.Request) {
	// validate request method
	if req.Method != "GET" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// write response
	pong := []byte("Pong.\n")

	if bytes, err := w.Write(pong); err != nil || bytes != len(pong) {
		w.WriteHeader(http.StatusInternalServerError)
	}
}

func (t *TelePyth) PollUpdates() {
	offset := 0

	for {
		updates, err := t.Api.GetUpdates(offset, 100, t.Timeout, nil)

		if err != nil {
			//  TODO: more logging
			log.Println(err)
		}

		for _, update := range updates {
			t.HandleTelegramUpdate(&update)

			if update.UpdateId >= offset {
				offset = update.UpdateId + 1
			}
		}
	}
}

func (t *TelePyth) Serve() error {
	// run go-routing for long polling
	if t.Polling {
		log.Println("poling:", t.Polling)
		log.Println("timeout: ", t.Timeout)

		go t.PollUpdates()
	}

	// run http server
	mux := http.NewServeMux()
	mux.HandleFunc("/api/notify/", t.HandleNotifyRequest)
	mux.HandleFunc("/api/ping/", t.HandlePingRequest)
	mux.HandleFunc("/api/webhook/"+t.Api.GetToken(), t.HandleWebhookRequest)

	srv := http.Server{
		Addr:    ":8080",
		Handler: mux,
	}

	return srv.ListenAndServe()
}
