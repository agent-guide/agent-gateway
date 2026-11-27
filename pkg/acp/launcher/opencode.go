package launcher

func openCodeLauncher() Launcher {
	return Launcher{
		Command: "opencode",
		Args:    []string{"acp"},
		CWDFlag: "--cwd",
	}
}
