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
	Long: `
	It is a CLI utility, which uses a CSV file to check for 
	Node dependencies, and if not matching update them, finally 
	creating a PR for the same. A config.json file needs to be present 
	in the directory for proper functionality of the tool. Else use -c 
	to pass the configuration file path (needs to be JSON format). 
	
	For example:

		depmgmt --input=./dependencies.csv --version=@latest --update
		depmgmt -c ./config.json -i ./dependencies.csv -v @latest -u 
		`,
	Run: func(cmd *cobra.Command, args []string) {
		/*
			This is the main function which is called when the command is executed.
			FUNCTIONALITY:
				1. Read the CSV file and create a list of repositories
				2. Read the package.json file for each repository and create a map
				3. Check if the dependencies are matching with the CSV file
				4. If not matching update the dependencies
				5. Create a PR for the same
				6. If the PR is created successfully, update the CSV file with the new version
				7. If the PR is not created successfully, print the error message
		*/

		// Read all the flags and store them in variables
		input, _ := cmd.Flags().GetString("input")
		versionCheck, _ := cmd.Flags().GetString("version")
		update, _ := cmd.Flags().GetBool("update")
		config, _ := cmd.Flags().GetString("config")

		fileType := input[strings.LastIndex(input, ".")+1:]
		var res []repo
		if fileType == "csv" {
			// Read the CSV file and create a list of repositories
			data := readCSVfile(input)
			for _, d := range data {
				// Read the package.json file for each repository and create a map of the data
				url := d.URL
				repoName := url[strings.LastIndex(url, "/")+1:]
				userURL := url[:strings.LastIndex(url, "/")]
				userName := userURL[strings.LastIndex(userURL, "/")+1:]

				// Check if the dependencies are matching with the CSV file
				dependencies := packageJSONMap(url, config)["dependencies"]
				depCheckVersion := versionCheck[strings.LastIndex(versionCheck, "@")+1:]
				depCheckName := versionCheck[:strings.LastIndex(versionCheck, "@")]

				if _, ok := dependencies.(map[string]interface{})[depCheckName]; ok {
					// If the dependency is present in the package.json file, check if the version is matching
					currVersion := dependencies.(map[string]interface{})[depCheckName].(string)
					currVersion = strings.Replace(currVersion, "^", "", -1)

					d.version = currVersion
					d.Name = repoName

					if currVersion != depCheckVersion {
						// If the version is not matching, update the dependency in the package.json file and create a PR for the same
						fmt.Println("\n" + d.Name + ": " + currVersion + " is not the latest version. Please update it to " + depCheckVersion)
						if update {
							prURL := updateDep(url, userName, repoName, depCheckName, depCheckVersion, config)
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

			// Print the results in a table
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

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func updateDep(url string, userName string, repoName string, depName string, depVersion string, configPath string) string {
	/*
		This function is used to update the dependency in the package.json file and create a PR for the same.
		FUNCTIONALITY:
			1. Read the package.json file
			2. Update the dependency in the package.json file
			3. Create a PR for the same
			4. If the PR is created successfully, update the CSV file with the new version
			5. If the PR is not created successfully, print the error message
	*/

	ctx := context.Background()
	configJSON := readConfigJSON(configPath)
	auth_token := fmt.Sprint(configJSON["AUTH_TOKEN"])

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: auth_token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	tokenUser := fmt.Sprint(configJSON["TOKEN_USER"])

	// Check if the Authorised user has the permission to update the dependency in the package.json file else create a new Fork
	repo, _, _ := client.Repositories.Get(ctx, tokenUser, repoName)
	if repo.GetName() != repoName {
		fork := &github.RepositoryCreateForkOptions{}
		fmt.Println("Please wait while we fork the repo")
		client.Repositories.CreateFork(ctx, userName, repoName, fork)
		time.Sleep(time.Second * 5)
		fmt.Println("Forked the repo successfully.")
	}

	// In the fork repo, update the dependency in the package.json file and create a PR for the same
	userName = tokenUser
	repo, _, err := client.Repositories.Get(ctx, userName, repoName)

	if err != nil {
		log.Fatal(err)
	}

	branch := repo.GetDefaultBranch()
	repoURL := repo.GetHTMLURL()

	data := packageJSONMap(repoURL, configPath)
	data["dependencies"].(map[string]interface{})[depName] = "^" + depVersion
	jsonData, _ := json.Marshal(data)
	fileContent, _, _, err := client.Repositories.GetContents(ctx, userName, repoName, "package.json", nil)
	if err != nil {
		log.Fatal(err)
	}

	// Create a PR for the updated dependency
	commitMsg := "Update dependencies " + depName + " to " + depVersion
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

	// Check if PR already exists for the updated dependency, if yes, update the PR else create a new PR for the updated dependency
	prCheckURL := "https://api.github.com/repos/" + parentName + "/" + repoName + "/pulls?base=" + parentBranch + "&head=" + userName + ":" + branch
	prURLResponse, _ := http.Get(prCheckURL)
	prURLBody, _ := ioutil.ReadAll(prURLResponse.Body)

	if string(prURLBody) != "[]" {
		fmt.Println("Pull request already exists")
		content := bytes.NewBuffer(prURLBody)
		var prObj interface{}
		json.NewDecoder(content).Decode(&prObj)
		prURL := fmt.Sprint(prObj.([]interface{})[0].(map[string]interface{})["html_url"])
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

func packageJSONMap(URL string, configPath string) map[string]interface{} {
	/*
		This function is used to read the package.json file and return the map of the file
		FUNCTIONALITY:
			1. Read the package.json file
			2. Return the map of the file
	*/
	ctx := context.Background()
	configJSON := readConfigJSON(configPath)
	auth_token := fmt.Sprint(configJSON["AUTH_TOKEN"])

	ts := oauth2.StaticTokenSource(
		&oauth2.Token{AccessToken: auth_token},
	)
	tc := oauth2.NewClient(ctx, ts)
	client := github.NewClient(tc)

	repoName := URL[strings.LastIndex(URL, "/")+1:]
	userName := URL[:strings.LastIndex(URL, "/")]
	userName = userName[strings.LastIndex(userName, "/")+1:]

	fileContent, _, _, err := client.Repositories.GetContents(ctx, userName, repoName, "package.json", nil)
	if err != nil {
		log.Fatal(err)
	}

	content, _ := fileContent.GetContent()

	var data map[string]interface{}
	json.Unmarshal([]byte(content), &data)
	return data
}
func readConfigJSON(configPath string) map[string]interface{} {
	config, err := ioutil.ReadFile(configPath)
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
	rootCmd.Flags().StringP("config", "c", "config.json", "config file")
}
