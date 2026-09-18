-- Candidate native sender probe. Compiling this file does not execute it.
-- Sends only a generated test marker in a new work task; accepts no API input.
on run
    set marker to "DESKTOP_SCRIPT_" & (seconds of (current date)) & "_" & (random number from 100000 to 999999)
    set promptText to "这是桌面连通性测试。不要调用任何工具，只回复原文：" & marker
    set savedClipboard to the clipboard as record
    try
        tell application "System Events"
            set targets to every application process whose bundle identifier is "com.bot.pc.doubao"
            if (count of targets) is not 1 then error "Expected one running Doubao app"
            set targetApp to item 1 of targets
            tell targetApp
                set frontmost to true
                keystroke "j" using command down
                set ready to false
                repeat 50 times
                    if (count of windows) > 0 then
                        if name of front window is "豆包" then
                            set ready to true
                            exit repeat
                        end if
                    end if
                    delay 0.1
                end repeat
                if not ready then error "New work task was not observed; nothing submitted"
                set taskEditors to {}
                repeat with candidate in (entire contents of front window)
                    try
                        if value of attribute "AXRole" of candidate is "AXTextArea" then set end of taskEditors to candidate
                    end try
                end repeat
                if (count of taskEditors) is not 1 then error "Expected one message editor; nothing submitted"
                set taskEditor to item 1 of taskEditors
                set value of attribute "AXFocused" of taskEditor to true
                set the clipboard to promptText
                keystroke "v" using command down
                delay 0.2
                set actualText to value of attribute "AXValue" of taskEditor
                if actualText is not promptText then error "Editor content mismatch; nothing submitted"
                key code 36
            end tell
        end tell
    on error errorText number errorNumber
        set the clipboard to savedClipboard
        error errorText number errorNumber
    end try
    set the clipboard to savedClipboard
    return marker
end run
