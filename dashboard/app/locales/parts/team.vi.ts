// Vietnamese strings for team (source of truth for keys).
// A project's agents, picked from the library (ADR-099, ADR-166).
export default {
  'team.agentCount': '{n} agent',
  'team.noAgents': 'Chưa có agent',
  'team.default': 'Mặc định',
  'team.defaultHint': 'Agent này trả lời khi chat, tin nhắn bot (Discord/Telegram) hoặc một tự động hóa không chỉ rõ tên agent nào. Dùng khi project có nhiều agent nhưng muốn có một agent luôn trả lời trong các trường hợp chung chung. Đặt agent khác làm mặc định thì agent cũ thôi giữ vai trò này, chat cũ của nó vẫn giữ nguyên.',
  'team.defaultAgent': 'Agent mặc định',
  'team.defaultAgentLower': 'agent mặc định',
  'team.defaultSet': '{name} giờ là agent mặc định',
  'team.makeDefault': 'Đặt làm mặc định',
  'team.open': 'Mở trang agent',
  'team.export': 'Tải JSON các agent',
  'team.addAndConfigure': 'Thêm rồi cấu hình',
  'team.empty.title': 'Project chưa có agent',
  'team.empty.desc': 'Thêm agent từ thư viện, hoặc để AI chọn những gì project cần.',

  // AgentLibrary (ADR-153)
  'team.lib.addOne': 'Thêm một agent từ thư viện',
  'team.lib.all': 'Tất cả',
  'team.lib.pageTitle': 'Thư viện agent',
  'team.lib.pageDesc': 'Mẫu agent để thêm vào project hoặc tham khảo khi tự viết hướng dẫn. Bấm một agent để xem đầy đủ.',
  'team.lib.pickProject': 'Chọn project',
  'team.lib.toProject': 'Thêm vào project',
  'team.lib.featured': 'Đề xuất',
  'team.lib.cat.design': 'Thiết kế',
  'team.lib.count': '{n} agent',
  'team.lib.words': '{n} từ',
  'team.lib.agencyNote': 'Bản gốc tiếng Anh, khá dài: nên rút gọn sau khi thêm để đỡ tốn token',
  'team.lib.cat.code': 'Code',
  'team.lib.cat.plan': 'Kế hoạch',
  'team.lib.cat.customer': 'Khách hàng',
  'team.lib.cat.content': 'Nội dung',
  'team.lib.search': 'Tìm agent…',
  'team.lib.none': 'Không có agent nào khớp',
  'team.lib.source': 'Bản gốc',
  'team.lib.use': 'Dùng mẫu này',
  'team.lib.from': 'Theo mẫu {name}: kèm mô tả, hướng dẫn và quyền của mẫu',
  'team.lib.skills': 'Skill đi kèm:',
  'team.lib.requiresPack': 'Cần gói {pack}',
  'team.lib.packHelp': 'Mẫu này dùng workflow, skill và dữ liệu của gói project trên. Thêm riêng agent không cài các phụ thuộc đó. Trước khi dùng, kiểm tra project đã có chúng; nếu chưa, tạo project từ gói tương ứng hoặc điều chỉnh hướng dẫn cho project hiện tại.',
  'team.lib.skillsHelp': 'Agent giữ sẵn các skill này. Skill project chưa có sẽ được cài từ thư viện office vào thư mục skill của project.',
  'team.lib.back': 'Chọn mẫu khác',
  'team.rev.library': 'thêm từ thư viện: {arg}',
  'team.rev.setup': 'thiết lập bằng AI: {arg}',


  // RevisionHistory
  'team.rev.intro': 'Mỗi lần sửa agent, office lưu lại trạng thái ngay trước đó. Khôi phục một bản cũng được lưu, nên luôn hoàn tác được.',
  'team.rev.restoreConfirm': 'Đưa các agent của project về trạng thái này? Trạng thái hiện tại vẫn được lưu lại trong lịch sử.',
  'team.rev.pack': 'áp gói {arg}',

  // setup with AI
  'team.setup.hasAgentsWarn': 'Project đang có {n} agent, giữ nguyên. Thiết lập chỉ thêm từ thư viện những gì còn thiếu.',

  // home: getting started
  'team.home.step3Title': 'Cho project các agent',
  'team.home.step3TextDone': '{n}/{total} project đã có agent',
  'team.home.step3TextTodo': 'Chọn agent từ thư viện, hoặc để AI chọn',
} as const
