package main

import (
	"encoding/json"
	"fmt"
	"github.com/bmizerany/pat"
	"github.com/microcosm-cc/bluemonday"
	"github.com/shurcooL/github_flavored_markdown"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

type Configuration struct {
	Title     string
	Url       string
	Articles  string
	Templates string
	Host      string
	Frontpage string
	Editable  bool
}

type Article struct {
	Title      string
	ModifyDate time.Time
	Content    string
}

const (
	view_template      = "view.template"
	edit_template      = "edit.template"
	error_template     = "error.template"
	container_template = "main.template"
)

var templates map[string]string
var articles map[string]Article
var cfg *Configuration

// load all articles from a specific path
func load_articles(path string) {
	articles = make(map[string]Article)
	info, err := ioutil.ReadDir(path)

	if err != nil {
		log.Fatal("Failed to load articles: " + path)
	}

	for _, file := range info {
		isTemplate := strings.HasSuffix(file.Name(), ".md")

		if isTemplate {
			content_bytes, err := ioutil.ReadFile(path + file.Name())

			if err != nil {
				log.Fatal("Failed to read article: " + path + file.Name())
			}

			content := string(content_bytes)
			title := strings.Split(file.Name(), ".")[0]
			article := Article{title, file.ModTime(), content}

			articles[strings.ToLower(title)] = article
			fmt.Println("Loaded article " + file.Name())
		}
	}
}

// load all templates from a specific path
func load_templates(path string) {
	templates = make(map[string]string)
	info, err := ioutil.ReadDir(path)

	if err != nil {
		log.Fatal("Failed to load templates: " + path)
	}

	for _, file := range info {
		isTemplate := strings.HasSuffix(file.Name(), ".template")

		if isTemplate {
			content_bytes, err := ioutil.ReadFile(path + file.Name())

			if err != nil {
				log.Fatal("Failed to read template file: " + path + file.Name())
			}

			content := string(content_bytes)
			templates[file.Name()] = content
			fmt.Println("Loaded template " + file.Name())
		}
	}
}

// render markdown and sanitize the output
func render_markdown(md string) string {
	md_bytes := []byte(md)
	text_bytes := github_flavored_markdown.Markdown(md_bytes)
	sanitized_bytes := bluemonday.UGCPolicy().SanitizeBytes(text_bytes)
	return string(sanitized_bytes)
}

// render the specific template (not-recursive)
func render_template(template string, context map[string]string) string {
	start_time := time.Now().Nanosecond()

	result := templates[template]
	changed := true

	for changed {
		old_result := result
		for key, value := range templates {
			if key == template {
				continue
			}

			result = strings.Replace(result, "{"+key+"}", value, -1)
		}
		changed = old_result != result
	}

	for key, value := range context {
		result = strings.Replace(result, "{"+key+"}", value, -1)
	}

	diff_time := time.Now().Nanosecond() - start_time
	fmt.Printf("Needed %d nanoseconds to respond\n", diff_time)

	return result
}

func (art *Article) CreateContext() map[string]string {
	return map[string]string{
		"Wiki.Title":         cfg.Title,
		"Wiki.Url":           cfg.Url,
		"Article.Title":      art.Title,
		"Article.Content":    render_markdown(art.Content),
		"Article.RawContent": art.Content,
		"Article.ModifyDate": format_date(art.ModifyDate),
	}
}

func error_context(code int, name string, message string) map[string]string {
	return map[string]string{
		"Wiki.Title":    cfg.Title,
		"Wiki.Url":      cfg.Url,
		"Article.Title": name,
		"Error.Code":    fmt.Sprintf("%d", code),
		"Error.Message": message,
	}
}

func format_date(date time.Time) string {
	return date.Format("Mon Jan 2 15:04:05")
}

func handle_index(res http.ResponseWriter, req *http.Request) {
	http.Redirect(res, req, "/"+cfg.Frontpage, 301)
}

func handle_view(res http.ResponseWriter, req *http.Request) {
	article_name := strings.ToLower(req.URL.Query().Get(":article"))

	context := make(map[string]string)
	active_template := ""

	if article, exists := articles[article_name]; exists {
		context = article.CreateContext()
		active_template = view_template
	} else {
		context = error_context(200, "Not found", article_name + " was not found. You may want to <a href=\"" + article_name + "/edit\">create this page!</a>")
		active_template = error_template
	}

	context["content"] = render_template(active_template, context)
	fmt.Fprint(res, render_template(container_template, context))
}

func handle_edit(res http.ResponseWriter, req *http.Request) {
	article_name := strings.ToLower(req.URL.Query().Get(":article"))

	context := make(map[string]string)
	if article, exists := articles[article_name]; exists {
		context = article.CreateContext()
	} else {
		context = error_context(200, article_name, "Create the page")
		context["Article.RawContent"] = ""
	}
	context["content"] = render_template(edit_template, context)
	fmt.Fprint(res, render_template(container_template, context))
}

func handle_search(res http.ResponseWriter, req *http.Request) {
	fmt.Println("NONONO")
	if que, ok := req.URL.Query()["article"]; ok {
		if art, exists := articles[strings.ToLower(que[0])]; exists {
			http.Redirect(res, req, "/"+art.Title, 301)
			return
		}
	}

	context := error_context(404, "Page not found", "Sorry, the page was not found")
	res.WriteHeader(404)
	context["content"] = render_template(error_template, context)
	fmt.Fprint(res, render_template(container_template, context))
}

func handle_save(res http.ResponseWriter, req *http.Request) {
	article_name := strings.ToLower(req.URL.Query().Get(":article"))
	input_text := req.FormValue("textcontent")

	if len(input_text) > 0 {
		if article, ok := articles[article_name]; ok {
			err := ioutil.WriteFile(cfg.Articles+article.Title+".md", []byte(input_text), 0644)
			article.Content = input_text
			article.ModifyDate = time.Now()
			if err == nil {
				articles[article_name] = article
				http.Redirect(res, req, "/"+article.Title, 301)
				return
			}
		} else {
			valid_name, _ := regexp.MatchString("([A-Za-z\\-]{1,64})", article_name)
			if valid_name {
				active_article := Article{article_name, time.Now(), input_text}
				err := ioutil.WriteFile(cfg.Articles+active_article.Title+".md", []byte(input_text), 0644)
				if err == nil {
					articles[article_name] = active_article
					http.Redirect(res, req, "/"+active_article.Title, 301)
					return
				}
			}
		}
	}
	context := error_context(500, "Internal server error", "There happened something bad on the wiki server")
	res.WriteHeader(500)
	context["content"] = render_template(error_template, context)
	fmt.Fprint(res, render_template(container_template, context))
}

func load_config(path string) {
	file, err := os.Open(path)
	if err != nil {
		log.Fatal("Couldn't find configuration file: " + path)
	}
	decoder := json.NewDecoder(file)
	cfg = new(Configuration)
	err = decoder.Decode(cfg)
	if err != nil {
		log.Fatal(err)
	}
}

func main() {
	start_time := time.Now()

	load_config("config.json")
	load_articles(cfg.Articles)
	load_templates(cfg.Templates)

	mux := pat.New()
	mux.Get("/", http.HandlerFunc(handle_index))
	mux.Get("/search", http.HandlerFunc(handle_search))
	mux.Get("/:article", http.HandlerFunc(handle_view))
	if cfg.Editable {
		mux.Get("/:article/edit", http.HandlerFunc(handle_edit))
		mux.Post("/:article/save", http.HandlerFunc(handle_save))
	}

	http.Handle("/", mux)

	diff_time := float32(time.Now().Nanosecond()-start_time.Nanosecond()) / 1000000.0
	fmt.Printf("Server up and running after %f milliseconds\n", diff_time)

	// Run webserver
	log.Fatal(http.ListenAndServe(cfg.Host, nil))
}
