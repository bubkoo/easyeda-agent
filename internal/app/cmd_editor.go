package app

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"
)

// newEditorCmd returns the "editor" subcommand group: editor UI chrome around
// the canvas (side panels). It never changes the project.
func newEditorCmd(cfg *appConfig, stdout, stderr io.Writer) *cobra.Command {
	var window string
	editor := &cobra.Command{
		Use:   "editor",
		Short: "Editor UI around the canvas (side panels); never changes the project",
	}
	editor.PersistentFlags().StringVar(&window, "window", "", "EasyEDA window ID")

	// editor.panels — open/close the left/right side panels.
	var left, right string
	panels := &cobra.Command{
		Use:   "panels",
		Short: "Open or close the editor's left/right side panels",
		Long: "Open or close the left (project/library) and right (attributes/layers) side\n" +
			"panels through the official panel control. UI only — the project is untouched.\n" +
			"The SDK has no open/closed getter, so the result reports the call that was\n" +
			"made and each panel's lock (pin) state; a locked panel is reported, never\n" +
			"unlocked. Pair with `easyeda view fit` to centre the design afterwards.",
		Args: cobra.NoArgs,
		Example: `  easyeda editor panels --left closed --right closed && easyeda view fit
  easyeda editor panels --left open --right open`,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload := map[string]any{}
			for flag, value := range map[string]string{"left": left, "right": right} {
				switch value {
				case "":
				case "open", "closed":
					payload[flag] = value
				default:
					return fmt.Errorf("--%s must be open or closed, got %q", flag, value)
				}
			}
			if len(payload) == 0 {
				return fmt.Errorf("pass --left and/or --right (open|closed)")
			}
			return dispatch(cfg, "editor.panels", window, payload, stdout, stderr)
		},
	}
	panels.Flags().StringVar(&left, "left", "", "left panel: open | closed")
	panels.Flags().StringVar(&right, "right", "", "right panel: open | closed")
	editor.AddCommand(panels)
	return editor
}
