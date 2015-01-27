package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/emicklei/go-restful"
	"github.com/emicklei/go-restful/swagger"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

func main() {

	serverCmd := &cobra.Command{
		Run: func(cmd *cobra.Command, args []string) {
			shutdown := make(chan bool)
			hostport := startWebServer(shutdown)
			startBuilder(shutdown, hostport)
			<-shutdown
		},
	}

	serverCmd.Flags().String("data", "/data", "Path to store repos and other data")
	viper.BindPFlag("data", serverCmd.Flags().Lookup("data"))

	serverCmd.Execute()
}

func startWebServer(shutdown chan bool) (hostport string) {
	container := restful.NewContainer()
	for _, resource := range NewAPIResources() {
		resource.Register(container)
	}

	port := "8080"

	config := swagger.Config{
		WebServices:    container.RegisteredWebServices(),
		WebServicesUrl: "localhost:" + port,
		ApiPath:        "/apidocs.json",

		SwaggerPath:     "/apidocs/",
		SwaggerFilePath: "/home/sreed/git/3rdparty/swagger-ui/dist",
	}
	swagger.RegisterSwaggerService(config, container)

	server := &http.Server{Addr: ":" + port, Handler: container}

	go func() {
		err := server.ListenAndServe()
		shutdown <- true
		log.Fatal(err)
	}()

	return "localhost:" + port
}

func startBuilder(shutdown chan bool, hostport string) {
	go func() {
		for {
			time.Sleep(5 * time.Second)
			log.Println("Checking repos...")
			repos, err := listRepos(hostport)
			if err != nil {
				fmt.Println(err)
				continue
			}

			for k, repo := range repos {
				fmt.Printf("key: %v, repo: %+v\n", k, repo)

				if err := checkout(k, repo); err != nil {
					fmt.Printf("Error checking %v: %v\n", k, err)
					continue
				}
			}
		}
		shutdown <- true
	}()
}

func listRepos(hostport string) (repos map[string]Repo, err error) {
	url := fmt.Sprintf("http://%v/repos", hostport)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Non-200 response from %v: %v", url, resp.StatusCode)
	}

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(bytes, &repos); err != nil {
		return nil, err
	}

	return repos, nil
}

func checkout(id string, r Repo) error {
	if r.URL == "" {
		return fmt.Errorf("Repo has empty URL")
	}

	local := filepath.Join(os.TempDir(), "builder", id)

	if _, err := os.Stat(filepath.Join(local, ".git")); os.IsNotExist(err) {
		return clone(r.URL, local)
	}

	if err := os.MkdirAll(local, 0755); err != nil {
		return err
	}

	return nil
}

func clone(url, dest string) error {
	cloneCmd := exec.Command("git", "clone", url, dest)
	cloneCmd.Stderr = os.Stderr
	cloneCmd.Stdout = os.Stdout
	return cloneCmd.Run()
}
