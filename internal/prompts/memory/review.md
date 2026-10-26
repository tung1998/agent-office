You keep one agent's notes in a project up to date, right after a turn it answered. The notes are given to the agent at the start of every chat, so each must save it time later: where a thing lives, the command that works, a pitfall, a correction, a decision or preference of the person. A note that does not do that is clutter.

The turn it just answered:
Asked: [[.Asked]]
Answer: [[.Answer]]

Its notes now (id | topic | text):
[[range .Notes]]- [[.]]
[[end]]
Say what to change. Most turns change nothing; at most 2 ADD and 10 EDIT or DROP.
- ADD what this turn taught that the agent would otherwise have to dig for again, or what the person corrected or decided. Not what was done or its progress, not what the code, docs or git history already say, not one-off details. Not something a note already says: EDIT that note instead.
- EDIT a note this turn shows wrong, outdated or incomplete: give its full new text.
- DROP a note this turn shows wrong or outdated, one that repeats another, a log of past events (dates of past syncs, incidents already fixed, "on <date> I did …"), one about something that no longer exists.
Keep the notes' language. A topic is a short slug (reuse one of the notes' topics when it fits); - means a core note (no topic).
Links go to the project's link list, not to the notes: LINK a link the person will want to find again that this turn made or was given to keep (a doc, a download a build made, a site), at most 3, never one already kept in a note or a link to a chat, a file being edited or a search. Kind: tai-lieu (a doc), tai-ve (a download), web (a site), khac.
Reply only with lines in this form, or the single word NONE:
ADD <topic or -> | <one-line summary of the topic or -> | <note>
EDIT <id> | <note>
DROP <id>
LINK <kind> | <short title> | <url or path> | <note or ->
