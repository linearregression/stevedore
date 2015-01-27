package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"path/filepath"
	"sync"

	"github.com/emicklei/go-restful"
	"github.com/spf13/viper"
)

var (
	// mu guards access to repos (and other data structures eventually)
	mu    sync.Mutex
	repos map[string]Repo
)

// APIResource is implemented by values that register endpoints with a
// restful.Container via the Register function.
type APIResource interface {
	Register(container *restful.Container)
}

// NewAPIResources returns all APIResources that will be provided by the api.
func NewAPIResources() []APIResource {
	r := newRepoResource()
	return []APIResource{r}
}

// Repo represents a git source code repository.
type Repo struct {
	URL        string
	LastCommit string
}

// RepoResource provides functions for storing and retrieving Repo metadata
// from persistent storage.
type RepoResource struct{}

// NewRepoResource creates a new RepoResource.
func newRepoResource() RepoResource {
	return RepoResource{}
}

// Register creates a restful.WebService and configures API routes for managing
// Repos.
func (r RepoResource) Register(container *restful.Container) {
	ws := new(restful.WebService)
	ws.
		Path("/repos").
		Doc("Manage Repos").
		Consumes(restful.MIME_JSON).
		Produces(restful.MIME_JSON)

	ws.Route(ws.GET("/").
		To(r.findAllRepos).
		Doc("get all repos").
		Operation("findAllRepos").
		Returns(200, "OK", map[string]Repo{}))

	ws.Route(ws.GET("/{repo-id}").
		To(r.findRepo).
		Doc("get a repo").
		Operation("findRepo").
		Param(ws.PathParameter("repo-id", "repo id").DataType("string")).
		Writes(Repo{}))

	container.Add(ws)
}

func (r RepoResource) findAllRepos(request *restful.Request, response *restful.Response) {
	mu.Lock()
	defer mu.Unlock()

	if err := loadRepos(); err != nil {
		response.AddHeader("Content-Type", "text/plain")
		response.WriteErrorString(http.StatusInternalServerError, "500: "+err.Error())
		return
	}
	response.WriteEntity(repos)
}

func writeErrorResponse(response *restful.Response, code int, message string) {
	response.AddHeader("Content-Type", "text/plain")
	response.WriteErrorString(code, message)
}

func (r RepoResource) findRepo(request *restful.Request, response *restful.Response) {
	mu.Lock()
	defer mu.Unlock()

	if err := loadRepos(); err != nil {
		writeErrorResponse(response, http.StatusInternalServerError, err.Error())
		return
	}
	id := request.PathParameter("repo-id")
	repo := repos[id]
	if len(repo.URL) == 0 {
		writeErrorResponse(response, http.StatusNotFound, "Repo could not be found.")
		return
	}
	response.WriteEntity(repo)
}

func loadRepos() error {
	dataDir := viper.GetString("data")
	if dataDir == "" {
		return fmt.Errorf("data not set")
	}

	jsonFile := filepath.Clean(dataDir + "/repos.json")
	file, err := ioutil.ReadFile(jsonFile)
	if err != nil {
		return err
	}

	repos = map[string]Repo{}
	json.Unmarshal(file, &repos)
	fmt.Printf("Loaded %v repos from %v\n", len(repos), jsonFile)
	return nil
}
