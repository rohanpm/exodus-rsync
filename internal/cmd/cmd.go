package cmd

import (
	"context"
	"path"
	"strings"

	"github.com/release-engineering/exodus-rsync/internal/args"
	"github.com/release-engineering/exodus-rsync/internal/conf"
	"github.com/release-engineering/exodus-rsync/internal/gw"
	"github.com/release-engineering/exodus-rsync/internal/log"
	"github.com/release-engineering/exodus-rsync/internal/rsync"
	"github.com/release-engineering/exodus-rsync/internal/walk"
)

var ext = struct {
	conf  conf.Interface
	rsync rsync.Interface
	gw    gw.Interface
	log   log.Interface
}{
	conf.Package,
	rsync.Package,
	gw.Package,
	log.Package,
}

func webURI(srcPath string, srcTree string, destTree string) string {
	// TODO: handle the different behaviors when / is or is not given for src/dest
	cleanSrcPath := path.Clean(srcPath)
	cleanSrcTree := path.Clean(srcTree)
	relPath := strings.TrimPrefix(cleanSrcPath, cleanSrcTree+"/")
	return path.Join(destTree, relPath)
}

// Main is the top-level entry point to the exodus-rsync command.
func Main(rawArgs []string) int {
	parsedArgs := args.Parse(rawArgs, nil)

	logger := ext.log.NewLogger(parsedArgs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ctx = log.NewContext(ctx, logger)

	cfg, err := ext.conf.Load(ctx, parsedArgs)
	if err != nil {
		logger.WithField("error", err).Error("can't load config")
		return 23
	}

	// TODO: mixed mode (run rsync AND exodus sync)

	env := cfg.EnvironmentForDest(ctx, parsedArgs.Dest)
	if env == nil {
		// just run rsync
		if err := ext.rsync.Exec(ctx, cfg, parsedArgs); err != nil {
			logger.WithField("error", err).Error("can't exec rsync")
			return 94
		}
	}

	gwClient, err := ext.gw.NewClient(*env)
	if err != nil {
		logger.F("error", err).Error("can't initialize exodus-gw client")
		return 101
	}

	var items []walk.SyncItem

	err = walk.Walk(ctx, parsedArgs.Src, func(item walk.SyncItem) error {
		items = append(items, item)
		return nil
	})
	if err != nil {
		logger.F("src", parsedArgs.Src, "error", err).Error("can't read files for sync")
		return 73
	}

	uploadCount := 0
	existingCount := 0

	err = gwClient.EnsureUploaded(ctx, items,
		func(uploadedItem walk.SyncItem) error {
			uploadCount++
			return nil
		},
		func(existingItem walk.SyncItem) error {
			existingCount++
			return nil
		},
	)

	if err != nil {
		logger.F("error", err).Error("can't upload files")
		return 25
	}

	logger.F("uploaded", uploadCount, "existing", existingCount).Info("Completed uploads")

	publish, err := gwClient.NewPublish(ctx)
	if err != nil {
		logger.F("error", err).Error("can't create publish")
		return 62
	}
	logger.F("publish", publish.ID()).Info("Created publish")

	publishItems := []gw.ItemInput{}

	for _, item := range items {
		publishItems = append(publishItems, gw.ItemInput{
			WebURI:    webURI(item.SrcPath, parsedArgs.Src, parsedArgs.DestPath()),
			ObjectKey: item.Key,
		})
	}

	err = publish.AddItems(ctx, publishItems)
	if err != nil {
		logger.F("error", err).Error("can't add items to publish")
		return 51
	}

	logger.F("publish", publish.ID(), "items", len(publishItems)).Info("Added publish items")

	err = publish.Commit(ctx)
	if err != nil {
		logger.F("error", err).Error("can't commit publish")
		return 71
	}

	logger.Info("Completed successfully!")

	return 0
}
