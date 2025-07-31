package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
)

type UpdaterResponse struct {
	Version   string            `json:"version"`
	PubDate   string            `json:"pub_date,omitempty"`
	Url       string            `json:"url"`
	FileName  string            `json:"fileName"`
	Libraries map[string]string `json:"libraries"`
}

func fetchLatestVersionWithAssets(repo, group, artifact string) (version, jarUrl, librariesUrl, pubDate string, err error) {
	nexusUrl := "https://maven.twelveiterations.com/service/rest/v1/search"
	query := fmt.Sprintf("repository=%s&group=%s&name=%s&sort=version", repo, group, artifact)
	url := nexusUrl + "?" + query

	resp, err := http.Get(url)
	if err != nil {
		return "", "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", "", "", "", fmt.Errorf("Nexus API error: %s", resp.Status)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", "", err
	}
	var data struct {
		Items []struct {
			Version string `json:"version"`
			Assets  []struct {
				DownloadUrl  string `json:"downloadUrl"`
				LastModified string `json:"lastModified,omitempty"`
				Maven2       struct {
					Classifier string `json:"classifier,omitempty"`
					Extension  string `json:"extension,omitempty"`
				} `json:"maven2"`
			} `json:"assets"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &data); err != nil {
		return "", "", "", "", err
	}
	if len(data.Items) == 0 {
		return "", "", "", "", fmt.Errorf("No items found in Nexus response")
	}
	item := data.Items[0]
	for _, asset := range item.Assets {
		if asset.Maven2.Classifier == "dist" && asset.Maven2.Extension == "jar" {
			jarUrl = asset.DownloadUrl
			pubDate = asset.LastModified
		}
		if asset.Maven2.Classifier == "libraries" && asset.Maven2.Extension == "json" {
			librariesUrl = asset.DownloadUrl
		}
	}
	if jarUrl == "" {
		return item.Version, "", "", "", fmt.Errorf("No jar asset found for latest version")
	}
	return item.Version, jarUrl, librariesUrl, pubDate, nil
}

func fetchAndParseLibrariesJson(assetUrl string) (map[string]string, error) {
	if assetUrl == "" {
		return nil, nil
	}
	resp, err := http.Get(assetUrl)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Failed to fetch libraries asset: %s", resp.Status)
	}
	var data struct {
		Libraries []struct {
			Group      string `json:"group"`
			Name       string `json:"name"`
			Version    string `json:"version"`
			Classifier string `json:"classifier"`
			Extension  string `json:"extension"`
		} `json:"libraries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return nil, err
	}
	libs := make(map[string]string)
	for _, lib := range data.Libraries {
		classifier := ""
		if lib.Classifier != "" {
			classifier = fmt.Sprintf(":%s", lib.Classifier)
		}
		extension := "jar"
		if lib.Extension != "" {
			extension = lib.Extension
		}
		fileName := fmt.Sprintf("%s-%s%s.%s", lib.Name, lib.Version, strings.ReplaceAll(classifier, ":", "-"), extension)
		libs[fileName] = fmt.Sprintf("https://maven.twelveiterations.com/repository/selene-public/%s/%s/%s/%s", strings.ReplaceAll(lib.Group, ".", "/"), lib.Name, lib.Version, fileName)
	}
	return libs, nil
}

func transformToPublicUrl(url string) string {
	return strings.ReplaceAll(strings.ReplaceAll(url, "maven-releases", "selene-public"), "maven-snapshots", "selene-public")
}

func extractFileName(url string) string {
	return strings.Split(url, "/")[len(strings.Split(url, "/"))-1]
}

func gameHandler(w http.ResponseWriter, r *http.Request) {
	segments := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	if len(segments) != 3 || segments[0] != "selene-client" || segments[2] != "latest.json" {
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}
	var repo string
	switch segments[1] {
	case "stable":
		repo = "maven-snapshots" // TODO for now, until we have a first stable release
	case "experimental":
		repo = "maven-snapshots"
	default:
		http.Error(w, "Not found", http.StatusNotFound)
		return
	}

	latestVersion, jarUrl, librariesUrl, pubDate, err := fetchLatestVersionWithAssets(repo, "world.selene", "selene-client")
	if err != nil {
		log.Printf("Warning: failed to fetch latest version: %v", err)
		http.Error(w, "Failed to fetch latest version", http.StatusInternalServerError)
		return
	}

	var libraries map[string]string
	if librariesUrl != "" {
		libraries, err = fetchAndParseLibrariesJson(transformToPublicUrl(librariesUrl))
		if err != nil {
			log.Printf("Warning: failed to parse libraries asset: %v", err)
		}
	} else {
		log.Printf("No libraries asset URL found")
	}

	resp := UpdaterResponse{
		Version:   latestVersion,
		PubDate:   pubDate,
		Url:       transformToPublicUrl(jarUrl),
		FileName:  extractFileName(jarUrl),
		Libraries: libraries,
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func main() {
	http.HandleFunc("/selene-client/", gameHandler)
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	log.Println("Serving endpoint at http://localhost:8080/selene-client/{branch}/latest.json")
	log.Fatal(http.ListenAndServe(":8080", nil))
}
