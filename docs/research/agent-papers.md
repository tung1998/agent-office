# ReAct, Reflexion, Generative Agents, Tree of Thoughts, Toolformer, Chain-of-Thought đối chiếu với office

Nguồn (các link rút gọn `lnkd.in` trong ảnh không mở được qua fetch trực tiếp; đã dò ra bằng số arXiv):
- ReAct: *ReAct: Synergizing Reasoning and Acting in Language Models* — arXiv:2210.03629 (Yao et al., 2022).
- Reflexion: *Reflexion: Language Agents with Verbal Reinforcement Learning* — arXiv:2303.11366 (Shinn et al., 2023).
- Generative Agents: *Generative Agents: Interactive Simulacra of Human Behavior* — arXiv:2304.03442 (Park et al., 2023).
- Tree of Thoughts: *Tree of Thoughts: Deliberate Problem Solving with Large Language Models* — arXiv:2305.10601 (Yao et al., 2023).
- Toolformer: *Toolformer: Language Models Can Teach Themselves to Use Tools* — arXiv:2302.04761 (Schick et al., 2023).
- Chain-of-Thought: *Chain-of-Thought Prompting Elicits Reasoning in Large Language Models* — arXiv:2201.11903 (Wei et al., 2022).

## Ý dùng được của từng bài (3-5 ý)

**ReAct (2210.03629)**
1. Xen kẽ Thought → Action → Observation từng bước, không tách rời suy luận và hành động.
2. Quan sát thật từ tool giảm ảo giác so với chỉ suy luận (CoT thuần) hoặc chỉ hành động (Act-only).
3. Vết suy luận hiện tường minh, con người (hoặc agent khác) sửa lỗi được giữa chừng, không phải chờ hết câu trả lời.
4. Tốt hơn Act-only rõ rệt trên việc cần tra cứu nhiều bước (multi-hop QA).

**Reflexion (2303.11366)**
1. Vòng Actor–Evaluator–Self-Reflection: không cập nhật trọng số model, chỉ "tăng cường" bằng văn bản (verbal reinforcement).
2. Self-reflection viết bài học bằng lời sau một lần thất bại, lưu bộ nhớ dài hạn, đưa vào ngữ cảnh của lần thử **tiếp theo cùng nhiệm vụ**.
3. Tín hiệu đánh giá ngoài (unit test, lệnh kiểm thật) đáng tin hơn để model tự chấm điểm chính mình.
4. Có giới hạn số vòng thử lại — không lặp vô hạn, dừng và báo khi hết vòng.
5. Cải thiện rõ trên việc cần thử-sai nhiều bước (coding, ra quyết định tuần tự).

**Generative Agents (2304.03442)**
1. Mỗi ký ức chấm 3 trục recency/importance/relevance, cộng điểm có trọng số để chọn đưa vào prompt.
2. Importance được chính LLM tự chấm điểm (1-10) ngay lúc ghi ký ức, không cần người gán.
3. Reflection định kỳ: khi điểm quan trọng dồn nén của ký ức gần đây vượt ngưỡng, agent tự hỏi vài câu hỏi lớn rồi tổng hợp một "insight" mới, lưu như ký ức cấp cao hơn.
4. Planning phân cấp: kế hoạch ngày → giờ → hành động cụ thể, có thể lập lại khi gặp sự kiện bất ngờ.

**Tree of Thoughts (2305.10601)**
1. Một bước có thể sinh nhiều "thought" ứng viên song song, không chỉ một chuỗi tuyến tính như CoT.
2. Có bước evaluator (tự chấm điểm hoặc so sánh/vote) để quyết đi tiếp nhánh nào, giống BFS/DFS có cắt tỉa.
3. Cho phép backtrack: bỏ nhánh tệ, quay sang nhánh khác thay vì cam kết một hướng duy nhất từ đầu.
4. Tốn nhiều lệnh gọi model hơn hẳn CoT — đánh đổi chi phí lấy độ chính xác trên bài toán cần tìm kiếm (Game of 24, đố chữ).

**Toolformer (2302.04761)**
1. Model tự học khi nào nên chèn lời gọi API vào văn bản, qua self-supervised fine-tuning, không cần người gán nhãn từng bước gọi tool.
2. Lọc lời gọi API giữ lại để huấn luyện bằng tiêu chí khách quan: có giúp giảm perplexity của câu kế tiếp hay không.
3. Chỉ cần vài ví dụ few-shot để bootstrap ra nhiều ví dụ gọi API rồi tự lọc, không cần tay làm toàn bộ dữ liệu.
4. Một model học dùng nhiều loại tool khác nhau cùng lúc (máy tính, tra cứu, lịch, dịch) và tự chọn đúng tool theo ngữ cảnh.

**Chain-of-Thought (2201.11903)**
1. Few-shot kèm chuỗi suy luận trung gian giúp model đủ lớn giải toán/suy luận nhiều bước tốt hơn hẳn trả lời trực tiếp.
2. Hiệu ứng là "emergent" — chỉ rõ rệt ở model đủ lớn, model nhỏ ít hoặc không lợi.
3. Không cần fine-tune, chỉ cần đổi cách viết ví dụ trong prompt (few-shot).
4. Là nền tảng mà ReAct/ToT mở rộng thêm (ReAct thêm hành động thật, ToT thêm nhánh cây và evaluator).

## 1. Trí nhớ (Generative Agents)

| Bài / Ý | Office đã có | File:dòng | Thiếu | Lợi ích |
|---|---|---|---|---|
| Mỗi ký ức được chấm 3 trục — **recency** (phân rã theo thời gian), **importance** (LLM chấm 1-10 lúc ghi), **relevance** (độ gần nghĩa với truy vấn hiện tại) — cộng điểm có trọng số để chọn ký ức đưa vào prompt (§Memory and Retrieval) | `memory.Recall` (`internal/memory/memory.go:494`) chỉ xếp theo **relevance** từ-khóa (idf qua `textrank.Rank`) và **recency** thô (khi không có từ khóa, lấy N ghi chú mới nhất — `internal/memory/memory.go:539-540`); không có trục importance | `internal/memory/memory.go:494-556` (`Recall`), `internal/memory/memory.go:401` (`Render`: core notes theo thứ tự ghi, không theo điểm) | Không có điểm "importance" gắn trên từng note khi ghi (`remember`) hay khi review (`Op{Op:"add"}` ở `internal/memory/memory.go:38`); mọi note cùng trọng số khi chọn đưa vào core/topic, trừ việc cũ hơn bị nén trước (`CompactTopic`) | Thêm field importance (ước lượng khi `remember`/review ghi note) để `Recall`/`Render` ưu tiên note quan trọng hơn thay vì chỉ mới/khớp từ khóa — tránh note nhỏ nhặt nhưng mới đẩy note quan trọng cũ ra khỏi core |
| **Reflection**: định kỳ (khi tổng importance ký ức gần đây vượt ngưỡng) agent tự hỏi 3 câu hỏi lớn rồi tổng hợp "insight" mới từ các ký ức liên quan, insight này lại được lưu làm ký ức cấp cao hơn (§Reflection) | Có cơ chế gần tương đương nhưng khác cơ chế kích hoạt: `Service.Review` (`internal/memory/memory.go:203`) chạy **sau mỗi lượt trả lời** (không theo ngưỡng tích lũy), dùng LLM (`Reviewer`) để ra `Op` thêm/sửa/xoá; Burn có `retro()` (`internal/burn/retro.go:195`) tổng hợp lessons **sau mỗi run** | `internal/memory/memory.go:203-243` (`Review`, chạy nếu `s.Auto` bật), `internal/burn/retro.go:191-220` (`lessonsBlock`, `keepLessons`) | Không có khái niệm "ngưỡng tích lũy importance → phản tư" như bài; trigger hiện tại là theo sự kiện (mỗi lượt / mỗi run), không theo mức độ dồn nén của các ký ức gần đây | Không cấp thiết để thêm — office vốn ít hội thoại dài liên tục như agent mô phỏng xã hội trong bài; trigger theo lượt/run đã đủ rẻ và đơn giản hơn ngưỡng tích luỹ |

## 2. Tự sửa sai (Reflexion)

| Bài / Ý | Office đã có | File:dòng | Thiếu | Lợi ích |
|---|---|---|---|---|
| Vòng lặp Actor–Evaluator–Self-Reflection: Actor thử, Evaluator chấm (binary hoặc điểm), Self-Reflection viết một **bài học bằng lời** ("tôi đã lặp lại lệnh X ba lần, lẽ ra phải kiểm Y trước") rồi lưu vào bộ nhớ dài hạn, bài học này được đưa vào ngữ cảnh của lần thử **tiếp theo cùng nhiệm vụ** (§2 Reflexion) | Đúng tinh thần này ở cấp **run** (không phải cấp từng việc riêng lẻ): `internal/burn/retro.go:195-220` (`retro`) đọc thống kê một run Burn kết thúc, LLM viết lại khối lessons, `keepLessons` thay toàn bộ lessons cũ của Burn này; lessons được chèn vào prompt của **lượt scan kế tiếp** (thấy ở `retroPrompt`, dòng 183) | `internal/burn/retro.go:183-220`, lessons hiện diện ở "Lessons this Burn learned" đầu mỗi piece (đúng là ngữ cảnh prompt tool đang thấy) | Reflexion phản tư *mỗi lần thử một nhiệm vụ cụ thể thất bại rồi thử lại ngay nhiệm vụ đó*; office phản tư *sau khi cả một run (nhiều piece) kết thúc*, không có vòng ngắn "piece X thất bại → viết bài học cho đúng piece X → piece X thử lại ngay". Việc thử lại một piece cụ thể do `maxTries`+review quyết, nhưng không gắn bài học *của riêng piece đó* | Xem đề xuất 1 — rút ngắn vòng phản tư xuống cấp từng piece thay vì chờ hết run |
| ADR-262 (cố vấn) đã ghi trong bài so sánh trước (`docs/research/openai-google-agents.md` dòng 20) là "AI hỏi AI khác vendor một lần khi lặp lỗi trong **cùng lượt**" — gần với Self-Reflection của Reflexion nhưng câu trả lời của cố vấn không được **lưu lại** làm bài học cho lần sau, chỉ hiển thị cho lượt đó | `internal/chat/advice.go:21-23` (tracker đếm lỗi lặp, gọi cố vấn một lần, lưu `storage.Advice` cho lượt đó) | `internal/chat/advice.go` (không thấy ghi `Advice.Answer` vào `internal/memory`) | Lời khuyên của cố vấn không tự động hoá thành một note trong `internal/memory` (dù agent có thể tự `remember` nếu nó đọc và thấy đáng) — Reflexion lưu bài học *tự động*, không chờ agent chủ động quyết định ghi hay không | Không đề xuất quest riêng — rủi ro trùng với đề xuất 1 (gắn kết lessons theo piece) nếu làm cả hai, chọn 1 trong 2 là đủ |
| Goal rounds của automation (ADR-132): một chat/workflow chạy lại nhiều vòng cho tới khi lệnh kiểm (`Check`) và/hoặc agent giám khảo (`Judge`) đồng ý mục tiêu đã đạt, tối đa `MaxGoalRounds` vòng — đây là vòng Reflexion **sát nhất** trong office: thử lại đúng cùng nhiệm vụ trong cùng một chat, có một bước đánh giá (check hoặc judge) trước khi thử vòng kế | `internal/trigger/goal.go:23-24` (`DefaultGoalRounds = 5`, `MaxGoalRounds = 20`), `internal/trigger/goal.go:136` (prompt `trigger/goal-again` cho vòng kế tiếp), `internal/trigger/goal.go:211-262` (`goalCheck`: chạy `Check`, rồi `Judge` nếu check qua, cắt phản hồi còn lại `goalFeedback = 4000` byte ở dòng 225/258/293) | Vòng sau chỉ nhận lại **output/judge thô bị cắt** (`truncateTail(..., goalFeedback)`), không có bước LLM tự viết một "bài học" cô đọng như Self-Reflection của Reflexion (vd "tôi đã sai ở bước X vì Y, lần này cần Z"); và bài học đó (nếu có) không được giữ lại cho **các lần chạy automation sau** của cùng automation — mỗi lần automation chạy lại từ đầu, không tích luỹ qua các run như Burn lessons | Xem đề xuất 2 (thay cho ý "thêm ví dụ ToT vào tài liệu workflow" ở bản trước) — đây là khoảng trống Reflexion rõ hơn, có file:dòng cụ thể và lợi ích thật (đỡ lặp lại đúng lỗi cũ giữa các lần automation chạy) |

## 3. Lập kế hoạch & thử nhiều hướng (ReAct, Tree of Thoughts, Toolformer, CoT)

| Bài / Ý | Office đã có | File:dòng | Thiếu | Lợi ích |
|---|---|---|---|---|
| ReAct: xen kẽ **Thought → Action → Observation** từng bước một, mỗi quan sát từ công cụ thật nuôi lại bước suy nghĩ kế tiếp (không phải suy nghĩ hết rồi mới hành động — §3) | Đúng mô hình vòng lặp chat (agent gọi tool, nhận kết quả, gọi tool tiếp) — đây là kiến trúc nền tảng của mọi agent trong office, không phải một tính năng cụ thể cần đối chiếu file:dòng | — (kiến trúc chung của engine chat, không phải một module riêng) | Không có khoảng trống cụ thể — đây là baseline mọi LLM-agent hiện đại đều theo, không phải phát hiện mới từ bài | — |
| Tree of Thoughts: khám phá **nhiều nhánh suy nghĩ song song** cho cùng một bước, có bước tự chấm điểm/so sánh các nhánh (evaluator) rồi chọn nhánh tốt nhất để đi tiếp (BFS/DFS trên cây) — khác hẳn ReAct (một đường thẳng) (§3-4) | `Workflow` (`internal/workflow`) có `Parallel` (`internal/workflow/workflow.go:51` field `Parallel [][]string`) chạy nhiều agent trong cùng một batch độc lập, nhưng không có bước **so sánh/chọn nhánh tốt nhất** tích hợp sẵn — việc "pipeline rồi verify" trong hướng dẫn viết workflow (ví dụ mẫu `review-changes` ở mô tả tool Workflow) là do người viết script tự ráp `pipeline`/`parallel`, không phải một primitive ToT có sẵn (branch→score→prune) | `internal/workflow/workflow.go:51` (`Parallel`), `internal/workflow/steps.go:173` (`Step.Pick`, chọn nhánh kế tiếp theo điều kiện đã biết trước, không phải theo điểm agent tự chấm) | Không có "evaluator chọn nhánh tốt nhất trong N nhánh được sinh song song" như một khối dựng sẵn — `Step.Pick` rẽ nhánh theo biến đã có (if/else), không phải theo một agent chấm điểm nhiều kết quả song song rồi chọn | Không cấp thiết thành quest riêng — xem phần "Đề xuất" bên dưới (ưu tiên khoảng trống goal rounds hơn); pattern này có thể viết trực tiếp bằng `parallel()`/`agent()` đã có khi cần, không chặn gì |
| ToT còn có cơ chế **vote/so sánh giữa các nhánh bởi nhiều "giám khảo"** để quyết nhánh nào đi tiếp (self-evaluation qua vote, không chỉ một evaluator duy nhất, §3) — Workflow của office có cơ chế **gần nhất, nhưng khác mục đích**: `Vote` là nhiều *vai* (role, mỗi vai một agent cố định từ đầu) cùng biểu quyết **đồng ý/từ chối một bước đã xong** (có `Quorum` và `Veto`), không phải nhiều *nhánh lời giải* cạnh tranh nhau cho cùng một việc rồi chọn 1 nhánh để tiếp tục — ToT vote để **chọn hướng đi tiếp**, Workflow vote để **duyệt/chặn một bước** (giống cơ chế approval nhiều người hơn là tìm kiếm trên cây) | `internal/workflow/workflow.go:140-144` (`type Vote struct{ Roles, Quorum, Veto }`), `internal/workflow/workflow.go:436-452` (validate: cần ≥2 vai, `Quorum` 1..len(Roles), `Veto` phải nằm trong `Roles`), `internal/workflow/steps.go:229-233` (`Vote` chỉ giữ lại ở một "coordinate def" nếu mọi vai bỏ phiếu còn trong tập vai giữ lại) | Không có "vote để chọn nhánh lời giải tốt nhất trong N nhánh độc lập" kiểu ToT — `Vote` hiện chỉ dùng cho việc duyệt/chặn (giống gate `approve`), không dùng để so sánh nhiều *kết quả* khác nhau của cùng một việc rồi chọn 1 | Không cấp thiết thành quest riêng — hai cơ chế phục vụ hai mục đích khác nhau (duyệt vs tìm kiếm-chọn-nhánh), không phải một khoảng trống cần vá; nếu cần "chọn nhánh tốt nhất", `parallel()` + một agent chấm điểm cuối (không cần `Vote`) đã đủ |
| Toolformer: model **tự quyết định khi nào cần gọi tool** và tự sinh lời gọi API phù hợp, học qua self-supervision (không cần người gán nhãn mỗi bước gọi tool) (§2) | office không huấn luyện model — dùng model nền có sẵn khả năng tool-calling; phần "tự quyết khi nào gọi tool" là hành vi suy luận của model, không phải thứ office code; office chỉ định nghĩa *tập tool nào được thấy* theo quyền (`internal/perm`) | — | Không áp dụng — Toolformer là kỹ thuật huấn luyện model, nằm ngoài phạm vi một ứng dụng dùng model qua API | — |
| Chain-of-Thought: few-shot prompt kèm chuỗi suy luận trung gian giúp model giải bài toán nhiều bước tốt hơn, không cần fine-tune (§1, §3) | Prompt của office (`internal/prompts`) là instruction trực tiếp, không phải few-shot CoT có ví dụ minh hoạ từng bước; model nền hiện đại (Claude, GPT) đã có suy luận nội tại mạnh hơn nhu cầu CoT-prompt thời 2022 | `internal/prompts` (thư mục template) | Không có ví dụ few-shot CoT trong các prompt hệ thống — nhưng đây là kỹ thuật dành cho model yếu hơn thời điểm bài ra đời, ít còn cấp thiết với model nền hiện tại | Không đề xuất — rủi ro prompt dài hơn không đáng so với lợi ích biên rất nhỏ trên model hiện đại |

## Đề xuất (tối đa 3)

1. **Gắn bài học vào đúng piece thất bại, không chỉ dồn vào lessons chung cuối run** — Reflexion viết bài học ngay sau một lần thử thất bại, cho đúng nhiệm vụ đó thử lại; office hiện ghi "Lessons this Burn learned" dùng chung cho mọi piece của Burn tiếp theo (`internal/burn/retro.go:195-220`), nhưng khi một piece failed và được record lại để thử (trong `maxTries` lần, `internal/burn/retro.go:34`), lần thử sau không có ghi chú riêng "piece dạng này trước đó sai ở đâu" gắn kèm — phải chờ hết cả run rồi lessons chung mới cập nhật. → Đã thêm quest `[Tích hợp] Gắn lý do fail riêng của piece vào lần record lại, không chờ lessons cuối run`.

2. **Goal rounds (ADR-132) nên tự viết một "bài học" cô đọng giữa các vòng, không chỉ chuyển tiếp output/judge thô bị cắt** — đây là vòng Reflexion sát nhất trong office (`internal/trigger/goal.go:211-262`, `goalCheck`) nhưng khác Reflexion ở đúng điểm cốt lõi: Reflexion có một bước LLM tổng hợp "tôi sai ở đâu, lần sau cần làm gì khác" trước khi đưa cho Actor thử lại; office hiện chỉ cắt 4000 byte cuối (`goalFeedback`, dòng 225/258/293) đưa thẳng cho vòng sau, không tổng hợp. Khoảng trống này rõ hơn và có file:dòng cụ thể hơn ý "thêm ví dụ ToT vào tài liệu" (vốn chỉ là việc viết tài liệu, không sửa hành vi thật) nên thay cho đề xuất đó. → Đã thêm quest `[Tích hợp] Goal rounds: viết bài học ngắn giữa các vòng thay vì chỉ chuyển tiếp output thô`. (Ý "ví dụ ToT vào workflow-authoring" vẫn còn mở dưới dạng idea đã ghi ở một bản trước của file này, `bit_01m6s6t4y52sbwxy5sh3y26vbh` — không rút lại, nhưng không tính vào 3 đề xuất chính của bản này vì ưu tiên thấp hơn goal rounds.)

3. **Không đề xuất thêm trục "importance" cho memory (Generative Agents)** — đúng là office chỉ có recency+relevance, thiếu importance theo bài; nhưng thêm một điểm importance ước lượng bởi LLM mỗi lần `remember`/review sẽ tăng chi phí (một lần gọi model nữa) cho một lợi ích chưa rõ ràng ở quy mô ghi chú hiện tại của office (vài chục note/agent, không phải hàng nghìn ký ức như agent mô phỏng xã hội trong bài) — đây là đánh đổi sản phẩm (có đáng trả thêm cost để xếp hạng chất lượng hơn), không phải một việc nhỏ có file:dòng cụ thể. **Không tạo quest** cho đề xuất này. Tương tự, không tạo quest cho "vote chọn nhánh tốt nhất kiểu ToT" (mục 3 ở trên) vì `Vote` của Workflow phục vụ mục đích khác (duyệt/chặn một bước, không phải tìm kiếm trên cây) — không phải khoảng trống cần vá.

## Trạng thái quest đã tạo

Ghi chú (giống piece trước, `docs/research/openai-google-agents.md`): từ bên trong một piece Burn, tool `burn_quest`/`burn_quests`/`burn_assign`/`burn_drop` không được cấp (`internal/officetools/officetools.go:571-575`, lọc nhóm quest khi `burnChat(sc) == true`). Các đề xuất trên được ghi bằng `burn_add(kind="idea")`.

- `bit_01m6s6swmq0j1d92dmyh8hj1cb` — "[Tích hợp] Gắn lý do fail riêng của piece vào lần record lại, không chờ lessons cuối run"
- `bit_01m6s6t4y52sbwxy5sh3y26vbh` — "[Tích hợp] Thêm ví dụ ToT (N lời giải song song + agent chọn nhánh tốt nhất) vào workflow-authoring" (ghi ở bản trước, giữ lại, ưu tiên thấp hơn goal rounds ở trên)
- `bit_01m6s77tbf9bsdrpyf5p6zd6y9` — "[Tích hợp] Goal rounds: viết bài học ngắn giữa các vòng thay vì chỉ chuyển tiếp output thô"
