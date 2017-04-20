package state

import (
	"fmt"
	"time"

	"github.com/FactomProject/factomd/common/interfaces"
	"github.com/FactomProject/factomd/common/messages"
)

// BATCH_SIZE is the amount of blocks per torrent
const BATCH_SIZE uint32 = 250

type heightError struct {
	Err      error
	Sequence uint32
	Height   uint32
}

/**********************
 *       Uploads      *
 **********************/

// Controls the flow of uploading torrents
type UploadController struct {
	// DO NOT USE THIS MAP OUTSIDE sortRequests()
	// It is not concurrency safe
	uploaded           map[uint32]struct{} // Map of uploaded heights
	requestUploadQueue chan uint32
	sendUploadQueue    chan uint32      // heights to be uploaded
	failedQueue        chan heightError // Channel of heights that failed to upload

	DBStateManager interfaces.IManagerController

	quit chan int
}

func NewUploadController(dbsm interfaces.IManagerController) *UploadController {
	u := new(UploadController)
	u.requestUploadQueue = make(chan uint32, 100000) // Channel used if torrents enabled. Queue of torrents to upload
	u.sendUploadQueue = make(chan uint32, 100000)
	u.failedQueue = make(chan heightError, 1000)

	u.uploaded = make(map[uint32]struct{})

	u.quit = make(chan int, 10)
	u.DBStateManager = dbsm

	return u
}

func (s *State) RunUploadController() {
	fmt.Println("Starting upload controller")
	go s.Uploader.sortRequests()
	go s.uploadBlocks()
	go s.Uploader.handleErrors()
}

func (u *UploadController) Close() {
	u.quit <- 0
}

/*****************
	Go Routines
******************/

// sortRequests sorts through the inital requests to toss out repeats
func (u *UploadController) sortRequests() {
	for {
	backToTopSortRequests:
		select {
		// Avoid defering the lock, more overhead
		case s := <-u.requestUploadQueue:
			if _, ok := u.uploaded[s]; ok {
				// Already uploaded, toss out
				goto backToTopSortRequests
			}

			u.uploaded[s] = struct{}{}
			u.sendUploadQueue <- s
		case <-u.quit:
			u.quit <- 0
			return
		}
	}
}

func (s *State) uploadBlocks() {
	u := s.Uploader
	for {
	backToTopUploadBlocks:
		select {
		case <-u.quit:
			u.quit <- 0
			return
		default:
			readyFor := u.DBStateManager.RequestMoreUploads()
			// Need to check if we are able to upload any blocks. If we cannot, we will wait
			if readyFor == 0 { // Not ready for anything
				time.Sleep(1 * time.Second)
				goto backToTopUploadBlocks
			} else if readyFor < 0 {
				// This is a plugin crash....
				return
			} else {
				// We can make some uploads. Only loop readyFor times
				for i := 0; i < readyFor; i++ {
					select { // We will block if nothing is in queue and chill here
					case se := <-u.sendUploadQueue:
						err := s.uploadDBState(se)
						if err != nil {
							u.failedQueue <- heightError{Height: se * BATCH_SIZE, Sequence: se, Err: err}
						}
					case <-u.quit:
						u.quit <- 0
						return
					}
				}
			}
		}
	}
}

func (u *UploadController) handleErrors() {
	for {
		select {
		case <-u.quit:
			u.quit <- 0
			return
		case err := <-u.failedQueue:
			// Just retry in 2 seconds? We can't not do this.
			// fmt.Printf("UploadError %d: %s\n", err.Height, err.Err)
			time.Sleep(10 * time.Second)
			u.sendUploadQueue <- err.Sequence
		}
	}
}

/*****************
	State Calls
******************/

// Only called once to set the torrent flag.
func (s *State) SetUseTorrent(setVal bool) {
	s.useDBStateManager = setVal
}

func (s *State) UsingTorrent() bool {
	return s.useDBStateManager
}

/*****************
	Implementation for routines
******************/

// All calls get sent here and redirected into the uploadcontroller queue.
func (s *State) UploadDBState(dbheight uint32) {
	s.Uploader.requestUploadQueue <- dbheight / BATCH_SIZE
}

func (s *State) uploadDBState(sequence uint32) error {
	base := sequence * BATCH_SIZE
	// Create the torrent
	if s.UsingTorrent() {
		// When we complete height X+2, we can upload to it
		for (s.EntryDBHeightComplete - 2) < base+BATCH_SIZE {
			time.Sleep(2 * time.Second)
		}
		fullData := make([]byte, 0)
		var i uint32
		for i = 0; i < BATCH_SIZE; i++ {
			msg, err := s.LoadDBState(base + i)
			if err != nil {
				return err
			}
			if msg == nil {
				return fmt.Errorf("msg is nil")
			}
			d := msg.(*messages.DBStateMsg)
			//fmt.Printf("Uploading DBState %d, Sigs: %d\n", d.DirectoryBlock.GetDatabaseHeight(), len(d.SignatureList.List))
			block := NewWholeBlock()
			block.DBlock = d.DirectoryBlock
			block.ABlock = d.AdminBlock
			block.FBlock = d.FactoidBlock
			block.ECBlock = d.EntryCreditBlock

			eHashes := make([]interfaces.IHash, 0)
			for _, e := range d.EBlocks {
				block.AddEblock(e)
				for _, eh := range e.GetEntryHashes() {
					eHashes = append(eHashes, eh)
				}
			}

			if len(eHashes) == 0 {
				// No hashes in the msg. Possibly not make torrent?
				// If we only use torrents for entry syncing, then no need
				// to make this torrent
			}

			for _, e := range eHashes {
				if e.String()[:62] != "00000000000000000000000000000000000000000000000000000000000000" {
					//} else {
					ent, err := s.DB.FetchEntry(e)
					if err != nil {
						return fmt.Errorf("[2] Error creating torrent in SaveDBStateToDB: " + err.Error())
					}
					block.AddIEBEntry(ent)
				}
			}

			if len(d.SignatureList.List) == 0 {
				return fmt.Errorf("No signatures given, signatures must be in to be able to torrent")
			}
			block.SigList = d.SignatureList.List

			data, err := block.MarshalBinary()
			if err != nil {
				return fmt.Errorf("[3] Error creating torrent in SaveDBStateToDB: " + err.Error())

			}
			fullData = append(fullData, data...)
		}
		if s.IsLeader() {
			err := s.DBStateManager.UploadDBStateBytes(fullData, true)
			if err != nil {
				return fmt.Errorf("[TorrentUpload] Torrent failed to upload: %s\n", err.Error())
			}
		} else {
			// s.DBStateManager.UploadDBStateBytes(data, false)
		}
	}
	return nil
}

func (s *State) GetMissingDBState(height uint32) error {
	return s.DBStateManager.RetrieveDBStateByHeight(height)
}

func (s *State) SetDBStateManagerCompletedHeight(height uint32) error {
	return s.DBStateManager.CompletedHeightTo(height)
}