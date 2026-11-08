from investigator.assistant_binding import query_required, required_query_tool


def test_followup_requires_current_reads_instead_of_echoing_old_failures():
    context = {"message": "继续读取 CPU、内存、重启趋势和同期日志", "history": [{"role": "assistant", "content": {"summary": "Previous query failed"}}]}
    assert query_required(context, "incident_followup")
    assert required_query_tool([{"toolName": "get_assistant_context", "state": "succeeded", "result": {"data": context}}]) == "describe_analysis_dataset"
    context["message"] = "只解释已有证据，不要查询日志"
    assert not query_required(context, "incident_followup")
    context["message"] = "复盘已有结论"
    assert not query_required(context, "incident_followup")


def test_read_requirement_applies_to_the_current_job_and_stops_after_an_attempt():
    steps = [{"toolName": "describe_analysis_dataset", "state": "succeeded", "result": {"data": {"datasets": [{"requiredFilters": []}]}}}]
    assert required_query_tool(steps) == "execute_analysis_query"
    steps.append({"toolName": "execute_analysis_query", "state": "failed"})
    assert required_query_tool(steps) is None
