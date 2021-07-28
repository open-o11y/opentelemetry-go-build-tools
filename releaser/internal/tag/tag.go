// Copyright The OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tag

import (
	"fmt"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"log"

	tools "go.opentelemetry.io/build-tools"
	"go.opentelemetry.io/build-tools/releaser/internal/common"
)

func Run(versioningFile, moduleSetName, commitHash string, deleteModuleSetTags bool) {

	repoRoot, err := tools.FindRepoRoot()
	if err != nil {
		log.Fatalf("unable to change to repo root: %v", err)
	}

	t, err := newTagger(versioningFile, moduleSetName, repoRoot, commitHash, deleteModuleSetTags)
	if err != nil {
		log.Fatalf("Error creating new tagger struct: %v", err)
	}

	// if delete-module-set-tags is specified, then delete all newModTagNames
	// whose versions match the one in the versioning file. Otherwise, tag all
	// modules in the given set.
	if deleteModuleSetTags {
		if err := t.deleteModuleSetTags(); err != nil {
			log.Fatalf("Error deleting tags for the specified module set: %v", err)
		}

		fmt.Println("Successfully deleted module tags")
	} else {
		if err := t.tagAllModules(); err != nil {
			log.Fatalf("unable to tag modules: %v", err)
		}
	}
}

type tagger struct {
	common.ModuleSetRelease
	CommitHash plumbing.Hash
}

func newTagger(versioningFilename, modSetToUpdate, repoRoot, hash string, deleteModuleSetTags bool) (tagger, error) {
	modRelease, err := common.NewModuleSetRelease(versioningFilename, modSetToUpdate, repoRoot)
	if err != nil {
		return tagger{}, fmt.Errorf("error creating prerelease struct: %v", err)
	}

	fullCommitHash, err := getFullCommitHash(hash, modRelease.Repo)
	if err != nil {
			return tagger{}, fmt.Errorf("could not get full commit hash of given hash %v: %v", hash, err)
		}

	modFullTagNames := modRelease.ModuleFullTagNames()

	if deleteModuleSetTags {
		if err = verifyTagsOnCommit(modFullTagNames, modRelease.Repo, fullCommitHash); err != nil {
			return tagger{}, fmt.Errorf("verifyTagsOnCommit failed: %v", err)
		}
	} else {
		if err = modRelease.VerifyGitTagsDoNotAlreadyExist(); err != nil {
			return tagger{}, fmt.Errorf("VerifyGitTagsDoNotAlreadyExist failed: %v", err)
		}
	}

	return tagger{
		ModuleSetRelease: modRelease,
		CommitHash:       fullCommitHash,
	}, nil
}

func verifyTagsOnCommit(modFullTagNames []string, repo *git.Repository, targetCommitHash plumbing.Hash) error {
	var tagsNotOnCommit []string

	for _, tagName := range modFullTagNames {
		tagRef, tagRefErr := repo.Tag(tagName)

		switch tagRefErr {
		case nil:
			tagObj, tagObjErr := repo.TagObject(tagRef.Hash())
			if tagObjErr != nil {
				return fmt.Errorf("unable to get tag object: %v", tagObjErr)
			}

			tagCommit, tagCommitErr := tagObj.Commit()
			if tagCommitErr != nil {
				return fmt.Errorf("could not get tag object commit: %v", tagCommitErr)
			}

			if targetCommitHash != tagCommit.Hash {
				tagsNotOnCommit = append(tagsNotOnCommit, tagName)
			}

		case git.ErrTagNotFound:
			continue
		default:
			return fmt.Errorf("unable to fetch git tag ref for %v: %v", tagName, tagRefErr)
		}
	}

	if len(tagsNotOnCommit) > 0 {
		return &errGitTagsNotOnCommit{
			commitHash: targetCommitHash,
			tagNames: tagsNotOnCommit,
		}
	}

	return nil
}

func getFullCommitHash(hash string, repo *git.Repository) (plumbing.Hash, error) {
	fullHash, err := repo.ResolveRevision(plumbing.Revision(hash))
	if err != nil {
		return plumbing.ZeroHash, fmt.Errorf("error getting full hash: %v", err)
	}

	return *fullHash, nil
}

func (t tagger) deleteModuleSetTags() error {
	modFullTagsToDelete := t.ModuleSetRelease.ModuleFullTagNames()

	if err := t.deleteTags(modFullTagsToDelete); err != nil {
		return fmt.Errorf("unable to delete module tags: %v", err)
	}

	return nil
}

// deleteTags removes the tags created for a certain version. This func is called to remove newly
// created tags if the new module tagging fails.
func (t tagger) deleteTags(modFullTags []string) error {
	for _, modFullTag := range modFullTags {
		log.Printf("Deleting tag %v\n", modFullTag)

		if err := t.ModuleSetRelease.Repo.DeleteTag(modFullTag); err != nil {
			return fmt.Errorf("could not delete tag %v: %v", modFullTag, err)
		}
	}
	return nil
}

func (t tagger) tagAllModules() error {
	modFullTags := t.ModuleSetRelease.ModuleFullTagNames()

	tagMessage := fmt.Sprintf("Module set %v, Version %v",
		t.ModuleSetRelease.ModSetName, t.ModuleSetRelease.ModSetVersion())

	var addedFullTags []string

	log.Printf("Tagging commit %s:\n", t.CommitHash)

	for _, newFullTag := range modFullTags {
		log.Printf("%v\n", newFullTag)

		_, err := t.ModuleSetRelease.Repo.CreateTag(newFullTag, t.CommitHash, &git.CreateTagOptions{
			Message: tagMessage,
		})

		if err != nil {
			log.Println("error creating a tag, removing all newly created tags...")

			// remove newly created tags to prevent inconsistencies
			if delTagsErr := t.deleteTags(addedFullTags); delTagsErr != nil {
				return fmt.Errorf("git tag failed for %v: %v\n" +
					"During handling of the above error, failed to not remove all tags: %v",
					newFullTag, err, delTagsErr,
				)
			}

			return fmt.Errorf("git tag failed for %v: %v", newFullTag, err)
		}

		addedFullTags = append(addedFullTags, newFullTag)
	}

	return nil
}
