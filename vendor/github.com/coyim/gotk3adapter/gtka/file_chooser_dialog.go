package gtka

import (
	"github.com/gotk3/gotk3/gtk"
	"github.com/coyim/gotk3adapter/gtki"
)

type fileChooserDialog struct {
	*dialog
	internal *gtk.FileChooserDialog
}

func wrapFileChooserDialogSimple(v *gtk.FileChooserDialog) *fileChooserDialog {
	if v == nil {
		return nil
	}
	return &fileChooserDialog{wrapDialogSimple(&v.Dialog), v}
}

func wrapFileChooserDialog(v *gtk.FileChooserDialog, e error) (*fileChooserDialog, error) {
	return wrapFileChooserDialogSimple(v), e
}

func unwrapFileChooserDialog(v gtki.FileChooserDialog) *gtk.FileChooserDialog {
	if v == nil {
		return nil
	}
	return v.(*fileChooserDialog).internal
}

func (v *fileChooserDialog) GetFilename() string {
	return v.internal.GetFilename()
}

func (v *fileChooserDialog) SetCurrentName(v1 string) {
	v.internal.SetCurrentName(v1)
}
