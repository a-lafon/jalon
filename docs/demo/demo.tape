# Records demo-raw.gif. Run setup.sh first, then, from this folder: vhs demo.tape
# The hidden Sleeps cover the model's work: VHS's Wait+Screen does not read Claude
# Code's screen reliably. Lengthen one if a take shows the model still working.

Output demo-raw.gif
Set Shell zsh
Set FontFamily "Menlo"
Set FontSize 22
Set Width 1640
Set Height 1240
Set Padding 28
Set Margin 24
Set MarginFill "#F4F1EA"
Set WindowBar Colorful
Set BorderRadius 10
Set Framerate 20
Set TypingSpeed 28ms
Env COLORTERM truecolor

Hide
Type "clear && ./start.sh" Enter
Sleep 9s
Show
Sleep 1s

Type "Since this morning's deploy, PATCH /todos/:id returns 500 for some users. The prod log is in logs/server.log. Find the cause, don't fix it yet."
Sleep 600ms
Enter
Sleep 1.2s
Hide
Sleep 61s
Show
Screenshot s1.png
Sleep 7.5s

# The /compact line watch.sh copied from the transcript, pasted at once.
Hide
Set TypingSpeed 2ms
Paste
Set TypingSpeed 28ms
Sleep 1s
Show
Sleep 1.5s
Enter
Sleep 1s
Hide
Sleep 46s
Show
Screenshot s2.png
Sleep 1.5s

Type "Fix it with a test. Then plan moving the JSON file to SQLite (node:sqlite). I'll build it tomorrow."
Sleep 600ms
Enter
Sleep 1.2s
Hide
Sleep 151s
Show
Screenshot s3.png
Sleep 7.5s

Type "/clear"
Sleep 400ms
Enter
Sleep 1s
Type "/jalon-resume"
Sleep 600ms
Enter
Sleep 1.2s
Hide
Sleep 41s
Show
Screenshot s4.png
Sleep 6.5s
