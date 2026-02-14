// Tab switching between groups and deletions views
function showTab(tabName) {
    // Hide all tab content
    document.querySelectorAll('.tab-content').forEach(el => {
        el.classList.add('hidden');
    });

    // Show selected tab content
    const view = document.getElementById(`view-${tabName}`);
    if (view) {
        view.classList.remove('hidden');
    }

    // Refresh deletions list when switching to deletions tab
    if (tabName === 'deletions') {
        htmx.ajax('GET', '/api/deletions', {
            target: '#deletions-list',
            swap: 'innerHTML'
        });
    }

    // Update tab button styles
    document.querySelectorAll('[id^="tab-"]').forEach(btn => {
        btn.classList.remove('tab-active', 'border-blue-500', 'text-blue-600');
        btn.classList.add('tab-inactive', 'border-transparent', 'text-gray-500');
    });

    const activeTab = document.getElementById(`tab-${tabName}`);
    if (activeTab) {
        activeTab.classList.remove('tab-inactive', 'border-transparent', 'text-gray-500');
        activeTab.classList.add('tab-active', 'border-blue-500', 'text-blue-600');
    }
}

// Confirmation dialog for executing deletions
function confirmExecute() {
    if (confirm('Are you sure you want to delete all marked files? This action cannot be undone.')) {
        // Trigger the actual execution via htmx
        htmx.ajax('POST', '/api/deletions/execute', {
            target: '#deletions-result',
            swap: 'innerHTML',
            values: { dry_run: false }
        }).then(() => {
            // Refresh the deletions list after execution
            htmx.ajax('GET', '/api/deletions', {
                target: '#deletions-list',
                swap: 'innerHTML'
            });
        });
    }
}

// Modal management
function openModal(groupId) {
    const modal = document.getElementById('review-modal');
    if (modal) {
        modal.classList.remove('hidden');

        // Load group details into modal via htmx
        htmx.ajax('GET', `/api/groups/${groupId}`, {
            target: '#modal-content',
            swap: 'innerHTML'
        });
    }
}

function closeModal() {
    const modal = document.getElementById('review-modal');
    if (modal) {
        modal.classList.add('hidden');
        document.getElementById('modal-content').innerHTML = '';
    }
}

// Close modal on background click
document.addEventListener('DOMContentLoaded', () => {
    const modal = document.getElementById('review-modal');
    if (modal) {
        modal.addEventListener('click', (e) => {
            if (e.target === modal) {
                closeModal();
            }
        });
    }
});

// Format bytes for display
function formatBytes(bytes) {
    const sizes = ['B', 'KB', 'MB', 'GB', 'TB'];
    if (bytes === 0) return '0 B';
    const i = Math.floor(Math.log(bytes) / Math.log(1024));
    return Math.round(bytes / Math.pow(1024, i) * 10) / 10 + ' ' + sizes[i];
}

// Format timestamp for display
function formatTime(unixSec) {
    const date = new Date(unixSec * 1000);
    return date.toLocaleString();
}

// Show KEEP badge on selected file and update visual feedback
document.addEventListener('change', function(e) {
    if (e.target.name === 'keep_file_id') {
        // Hide all KEEP badges
        document.querySelectorAll('.radio-label').forEach(label => {
            label.classList.add('opacity-0');
            label.classList.remove('group-hover:opacity-60');
        });

        // Show badge on selected item (full opacity, no hover effect)
        const selectedLabel = e.target.closest('label').querySelector('.radio-label');
        if (selectedLabel) {
            selectedLabel.classList.remove('opacity-0');
        }

        // Reset all labels to default state
        document.querySelectorAll('label:has(input[name="keep_file_id"])').forEach(label => {
            label.classList.remove('border-green-500', 'bg-green-50');
            label.classList.add('border-gray-300');
        });

        // Highlight selected label
        const selectedLabelEl = e.target.closest('label');
        selectedLabelEl.classList.remove('border-gray-300');
        selectedLabelEl.classList.add('border-green-500', 'bg-green-50');
    }
});

// Handle mark group form submission manually (bypassing broken json-enc extension)
document.addEventListener('submit', function(e) {
    if (e.target.id === 'mark-group-form') {
        e.preventDefault();

        const form = e.target;
        const groupId = form.dataset.groupId;
        const selectedRadio = form.querySelector('input[name="keep_file_id"]:checked');

        if (!selectedRadio) {
            alert('Please select a file to keep');
            return;
        }

        // Convert value to integer as backend expects
        const keepFileId = parseInt(selectedRadio.value, 10);

        // Send JSON request manually
        fetch(`/api/groups/${groupId}/mark`, {
            method: 'POST',
            headers: {
                'Content-Type': 'application/json',
            },
            body: JSON.stringify({ keep_file_id: keepFileId })
        })
        .then(response => {
            if (response.ok) {
                console.log('SUCCESS - closing modal and refreshing');

                // Close the modal
                closeModal();

                // Refresh the groups list
                htmx.ajax('GET', '/api/groups?sort=wasted', {
                    target: '#groups-list',
                    swap: 'innerHTML'
                });

                // Always refresh deletions list (even if not currently visible)
                // so that when user switches to the tab, the data is fresh
                htmx.ajax('GET', '/api/deletions', {
                    target: '#deletions-list',
                    swap: 'innerHTML'
                });
            } else {
                response.json().then(data => {
                    alert('Error: ' + (data.error || 'Failed to mark group'));
                });
            }
        })
        .catch(error => {
            console.error('Request failed:', error);
            alert('Failed to submit: ' + error.message);
        });
    }
});

// Close modal and refresh lists after successful htmx form submission
// (Note: Currently not used since we handle form submission manually with fetch())
document.body.addEventListener('htmx:afterRequest', function(evt) {
    // Check if this was a successful mark group request
    if (evt.detail.successful &&
        evt.detail.xhr.responseURL.includes('/api/groups/') &&
        evt.detail.xhr.responseURL.includes('/mark')) {

        // Close the modal
        closeModal();

        // Refresh the groups list
        htmx.ajax('GET', '/api/groups', {
            target: '#groups-list',
            swap: 'innerHTML'
        });

        // Optionally refresh deletions list if visible
        const deletionsView = document.getElementById('view-deletions');
        if (deletionsView && !deletionsView.classList.contains('hidden')) {
            htmx.ajax('GET', '/api/deletions', {
                target: '#deletions-list',
                swap: 'innerHTML'
            });
        }
    }
});
