/*
Copyright © 2022 NAME HERE bgoel4132@gmail.com

*/
package cmd

import (
	"bufio"
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/go-github/v45/github"
	"github.com/jedib0t/go-pretty/table"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "depmgmt",
	Short: "CLI to manage your node dependencies, update them, and create a PR.",
	Long: `It is a CLI utility, which uses a CSV file to check for Node dependencies, and if not matching update them, finally creating a PR for the same. For example:

		depmgmt --input=./dependencies.csv --version=@latest --update
		`,
	Run: func(cmd *cobra.Command, args []string) {
		input, _ := cmd.Flags().GetString("input")
		versionCheck, _ := cmd.Flags().GetString("version")
		update, _ := cmd.Flags().GetBool("update")

		fileType := input[strings.LastIndex(input, ".")+1:]
		var res []repo
		if fileType == "csv" {
			var data = readCSVfile(input)
			for _, d := range data {
				var url = d.URL
				var repoName = url[strings.LastIndex(url, "/")+1:]
				var userURL = url[:strings.LastIndex(url, "/")]
				var userName = userURL[strings.LastIndex(userURL, "/")+1:]

				var packageJSONMap = packageJSONMap(url)
				var dependencies = packageJSONMap["dependencies"]
				var depCheckVersion = versionCheck[strings.LastIndex(versionCheck, "@")+1:]
				var depCheckName = versionCheck[:strings.LastIndex(versionCheck, "@")]

				if _, ok := dependencies.(map[string]interface{})[depCheckName]; ok {
					var currVersion = dependencies.(map[string]interface{})[depCheckName].(string)
					currVersion = strings.Replace(currVersion, "^", "", -1)
					d.version = currVersion
					d.Name = repoName
					if currVersion != depCheckVersion {
						fmt.Println("\n" + d.Name + ": " + currVersion + " is not the latest version. Please update it to " + depCheckVersion)
						if update {
							prURL := updateDep(url, userName, repoName, depCheckName, depCheckVersion)
							d.update_pr = prURL
						}
					} else {
						d.version_satisfied = true
					}
				} else {
					d.version = "Not found"
					d.Name = repoName
					d.version_satisfied = false
					d.update_pr = "Not found"
					fmt.Println("\n" + d.Name + ": " + "is not installed")
				}
				res = append(res, d)

			}

			tw := table.NewWriter()
			if update {
				tw.AppendHeader(table.Row{"Name", "Version", "Version Satisfied", "Update PR"})
			} else {
				tw.AppendHeader(table.Row{"Name", "Version", "Version Satisfied"})
			}

			for _, d := range res {
				if update {
					tw.AppendRow(table.Row{d.Name, d.version, d.version_satisfied, d.update_pr})
				} else {
					tw.AppendRow(table.Row{d.Name, d.version, d.version_satisfied})
				}
			}

			tw.SetStyle(table.StyleLight)
			tw.SetOutputMirror(os.Stdout)
			tw.Render()
		} else {
			fmt.Println("File extension not supported")
		}
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func updateDep(url string, userName string, repoName string, depName string, depVersion string) string {
	ctx := context.Background()
	var configJSON = readConfigJSON()
	var auth_token = fmt.Sprint(configJSON["AUTH_TOKEN"])

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: auth_token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	tokenUser := fmt.Sprint(configJSON["TOKEN_USER"])
	// Check if repoName exists in tokenUser account
	repos, _, _ := client.Repositories.Get(ctx, tokenUser, repoName)

	if repos.GetName() != repoName {
		fork := &github.RepositoryCreateForkOptions{}
		fmt.Println("Please wait while we fork the repo")
		client.Repositories.CreateFork(ctx, userName, repoName, fork)
		time.Sleep(time.Second * 5)
		fmt.Println("Forked the repo successfully.")
	}

	userName = tokenUser
	repo, _, err := client.Repositories.Get(ctx, userName, repoName)

	if err != nil {
		log.Fatal(err)
	}

	var branch = repo.GetDefaultBranch()
	var repoURL = repo.GetHTMLURL()

	var data = packageJSONMap(repoURL)
	data["dependencies"].(map[string]interface{})[depName] = "^" + depVersion
	var jsonData, _ = json.Marshal(data)
	var fileContent, _, _, err1 = client.Repositories.GetContents(ctx, userName, repoName, "package.json", nil)
	if err1 != nil {
		log.Fatal(err)
	}
	var commitMsg = "Update dependencies " + depName + " to " + depVersion
	file := &github.RepositoryContentFileOptions{
		Branch:  github.String(branch),
		Message: github.String(commitMsg),
		Committer: &github.CommitAuthor{
			Name:  github.String("Bhavya Goel"),
			Email: github.String("bgoel4132@gmail.com"),
		},
		Content: []byte(string(jsonData)),
		SHA:     github.String(fileContent.GetSHA()),
	}
	_, _, err = client.Repositories.UpdateFile(ctx, userName, repoName, "package.json", file)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Successfully updated the dependency")

	parentBranch := branch
	parentName := repo.GetOwner().GetLogin()

	parentRepo := repo.GetParent()
	if parentRepo != nil {
		parentBranch = parentRepo.GetDefaultBranch()
		parentName = parentRepo.GetOwner().GetLogin()
	}

	var prCheckURL = "https://api.github.com/repos/" + parentName + "/" + repoName + "/pulls?base=" + parentBranch + "&head=" + userName + ":" + branch
	var prURLResponse, _ = http.Get(prCheckURL)
	var prURLBody, _ = ioutil.ReadAll(prURLResponse.Body)

	if string(prURLBody) != "[]" {
		fmt.Println("Pull request already exists")
		var content = bytes.NewBuffer(prURLBody)
		var prObj interface{}
		json.NewDecoder(content).Decode(&prObj)
		var prURL = fmt.Sprint(prObj.([]interface{})[0].(map[string]interface{})["html_url"])
		return prURL
	} else {
		newPr := &github.NewPullRequest{
			Title:               github.String("Update dependency " + depName + " to " + depVersion),
			Head:                github.String(userName + ":" + branch),
			Base:                github.String(parentBranch),
			Body:                github.String("Update dependency " + depName + " to " + depVersion),
			MaintainerCanModify: github.Bool(true),
			Draft:               github.Bool(false),
		}

		pr, _, err := client.PullRequests.Create(ctx, parentName, repoName, newPr)
		if err != nil {
			log.Fatal(err)
		}

		return pr.GetHTMLURL()
	}
}

func packageJSONMap(URL string) map[string]interface{} {
	ctx := context.Background()
	var configJSON = readConfigJSON()
	var auth_token = fmt.Sprint(configJSON["AUTH_TOKEN"])

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: auth_token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	var repoName = URL[strings.LastIndex(URL, "/")+1:]
	var userName = URL[:strings.LastIndex(URL, "/")]
	userName = userName[strings.LastIndex(userName, "/")+1:]

	var fileContent, _, _, err = client.Repositories.GetContents(ctx, userName, repoName, "package.json", nil)
	if err != nil {
		log.Fatal(err)
	}

	var content, _ = fileContent.GetContent()

	var data map[string]interface{}
	json.Unmarshal([]byte(content), &data)
	return data
}
func readConfigJSON() map[string]interface{} {
	config, err := ioutil.ReadFile("config.json")
	if err != nil {
		log.Fatal(err)
	}

	var configJSON map[string]interface{}
	json.Unmarshal(config, &configJSON)
	return configJSON
}

type repo struct {
	Name              string
	URL               string
	version           string
	version_satisfied bool
	update_pr         string
}

func readCSVfile(input string) []repo {
	// open the file
	f, err := os.Open(input)
	if err != nil {
		fmt.Println(err)
	}
	defer f.Close()

	r := csv.NewReader(bufio.NewReader(f))
	r.Comma = ','
	r.Read()
	records, err := r.ReadAll()
	if err != nil {
		fmt.Println(err)
	}

	var repos []repo
	for _, record := range records {
		r := repo{
			Name: record[0],
			URL:  record[1],
		}
		repos = append(repos, r)
	}
	return repos
}

func init() {
	rootCmd.Flags().StringP("input", "i", "", "input file")
	rootCmd.Flags().StringP("version", "v", "", "version")
	rootCmd.Flags().BoolP("update", "u", false, "update")
}
