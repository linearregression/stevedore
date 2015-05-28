package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/zulily/stevedore/image"
	"github.com/zulily/stevedore/repo"
	"github.com/zulily/stevedore/slack"
	"github.com/zulily/stevedore/ui"
)

var (
	sleepDuration = 1 * time.Minute
	cfg           config
	notifications *slack.Slack
	repos         []*repo.Repo
	emptyJSON     = []byte("{}")
)

type config struct {
	sync.Mutex
	PublishCommand []string `json:"publishCommand"`
	RegistryURL    string   `json:"registryUrl"`
	Notifications  []string `json:"notifications"`
	Server         struct {
		Port     int    `json:"port"`
		TLS      bool   `json:"tls"`
		KeyFile  string `json:"keyFile"`
		CertFile string `json:"certFile"`
	}
	Slack struct {
		Channel  string `json:"channel"`
		Username string `json:"username"`
		Webhook  string `json:"webhook"`
	}
}

func loadConfig() error {
	cfg.Lock()
	defer cfg.Unlock()

	jsonFile := filepath.Clean("./config.json")
	file, err := ioutil.ReadFile(jsonFile)
	if err != nil {
		return err
	}

	err = json.Unmarshal(file, &cfg)
	if err != nil {
		return err
	}

	if len(cfg.PublishCommand) == 0 {
		cfg.PublishCommand = []string{"docker", "push"}
	}
	return nil
}

func main() {
	var err error
	err = loadConfig()
	if err != nil {
		ui.Err(err.Error())
		os.Exit(1)
	}

	ui.Info("loaded config")

	if contains(cfg.Notifications, "slack") && cfg.Slack.Webhook != "" {
		notifications, err = slack.New(
			slack.WithWebhook(cfg.Slack.Webhook),
			slack.WithChannelAndUsername(cfg.Slack.Channel, cfg.Slack.Username))
	}
	if err != nil {
		ui.Err(err.Error())
		return
	}

	http.HandleFunc("/", uiHandler)
	http.HandleFunc("/repos", handleRepoRequest)
	serveFile("/favicon.ico", "assets/favicon.ico")
	http.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("./assets/"))))

	go func() {
		address := fmt.Sprintf(":%d", cfg.Server.Port)
		ui.Info(fmt.Sprintf("starting web server on :%s", address))

		var err error
		if cfg.Server.TLS {
			ui.Info("using TLS")
			err = http.ListenAndServeTLS(address, cfg.Server.CertFile, cfg.Server.KeyFile, nil)
		} else {
			err = http.ListenAndServe(address, nil)
		}
		ui.Err(err.Error())
		os.Exit(1)
	}()

	for {
		check()
		//	ui.Task("%s repo images updated, built and published. Sleeping for %s...", strconv.Itoa(updated), sleepDuration.String())
		ui.Task("repo images updated, built and published. Sleeping for %s...", sleepDuration.String())
		time.Sleep(sleepDuration)
	}
}

// serveFile creates an http.HandleFunc that can serve a single static file
// from a specified path. This is useful for servicing requests for assets like
// "/favicon.ico" without having to serve a whole root directory.
func serveFile(pattern string, filename string) {
	http.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, filename)
	})
}

func uiHandler(w http.ResponseWriter, r *http.Request) {
	if err := RenderServicesHTML(repos, w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleRepoRequest(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "POST":
		handleRepoAdd(w, r)
	case "DELETE":
		handleRepoRemove(w, r)
	default:
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
	}
}

func handleRepoAdd(w http.ResponseWriter, r *http.Request) {
	var err error
	var rb struct {
		Repo string `json:"repo"`
	}

	decoder := json.NewDecoder(r.Body)
	if err = decoder.Decode(&rb); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	alreadyExists := false
	for _, r := range repos {
		if r.URL == rb.Repo {
			alreadyExists = true
			break
		}
	}

	if alreadyExists {
		http.Error(w, fmt.Sprintf("Repo: [%s] is already defined!", rb.Repo), http.StatusBadRequest)
		return
	}

	newRepo := &repo.Repo{
		URL: rb.Repo,
	}

	if err = newRepo.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	repos, err = repo.Add(newRepo)
	if err != nil {
		ui.Err(err.Error())
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(emptyJSON)
}

func handleRepoRemove(w http.ResponseWriter, r *http.Request) {
	var rb struct {
		Repo string `json:"repo"`
	}

	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&rb); err != nil {
		http.Error(w, http.StatusText(http.StatusBadRequest), http.StatusBadRequest)
		return
	}

	if rs, err := repo.Remove(rb.Repo); err != nil {
		http.Error(w, err.Error(), http.StatusNotModified)
		return
	} else {
		repos = rs
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(emptyJSON)
}

func check() (updated int) {
	ui.Task("Checking repos.")
	var err error
	repos, err = repo.All()

	if err != nil {
		ui.Err(err.Error())
		return 0
	}

	for _, r := range repos {
		r.Status = repo.StatusInProgress
		if checkRepo(r, cfg.RegistryURL) {
			r.Status = repo.StatusPassing
			updated++
		} else {
			r.Status = repo.StatusFailing
		}

		if err := r.Save(); err != nil {
			ui.Err(fmt.Sprintf("Error updating %s: %v", r.URL, err))
		}
	}

	return updated
}

func checkRepo(r *repo.Repo, registry string) bool {

	if err := r.Validate(); err != nil {
		ui.Warn(fmt.Sprintf("Skipping %s: %s", r.URL, err.Error()))
		return false
	}

	head, err := r.Checkout()
	if err != nil {
		ui.Err(fmt.Sprintf("Error checking %s: %v\n", r.URL, err))
		return false
	}

	if r.SHA == head {
		return true
	}
	ui.Info("%s has been updated from %s to %s. Starting a new build.", r.URL, r.SHA, head)

	// Update and persist the new SHA now, so that if a build/publish fails, it
	// won't repeate endlessly
	r.SHA = head

	if err := r.PrepareMake(); err != nil {
		ui.Err(fmt.Sprintf("Error preparing %s: %v", r.URL, err))
		return false
	}

	if output, err := image.Make(r); err != nil {
		msg := fmt.Sprintf("Error making %s: %v", r.URL, err)
		notify(msg, output, ui.Err)
		r.Log = output
		return false
	}

	results, err := image.Build(r, head, registry)
	if err != nil {
		ui.Err(err.Error())
		return false
	}

	// If an image build failure occurred, notify and capture the last N bytes of output
	for _, result := range results {
		if result.Err != nil {
			msg := fmt.Sprintf("Error building %s: %v", r.URL, result.Err)
			r.Log = result.Output[max(0, len(result.Output)-4000):len(result.Output)]
			notify(msg, result.Output, ui.Err)
			return false
		}
	}

	var images []string
	cmd := cfg.PublishCommand
	for _, result := range results {
		ui.Info("%s version %s has been built", r.URL, head)

		if output, err := image.Publish(result.ImageName, cmd); err != nil {
			msg := fmt.Sprintf("Error publishing %s: %v", r.URL, err)
			notify(msg, output, ui.Err)
			return false
		}

		msg := fmt.Sprintf("A new image for %s has been published to %s", r.URL, result.ImageName)
		notify(msg, "", ui.Info)
		images = append(images, result.ImageName)
	}

	// Save the images that were successfully published, along with a timestamp
	r.Images = images
	r.LastPublishDate = time.Now().Unix()
	r.Log = ""
	return true
}

// notify reports a msg to the specified ui output func, and to any configured
// notifications.  Additionally, if any errors ocurr during notificiation, that
// error is sent to the UI's Err func.
func notify(msg, output string, uiFunc func(string, ...string)) {
	uiFunc(msg)
	if notifications != nil {
		msg = fmt.Sprintf("%s\n%s", msg, output)
		if err := notifications.Notify(msg); err != nil {
			ui.Err(err.Error())
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// contains returns a boolean indicating whether or not `e` is contained in `s`
func contains(s []string, e string) bool {
	for _, a := range s {
		if a == e {
			return true
		}
	}
	return false
}
